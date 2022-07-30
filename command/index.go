package command

import (
	//	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/configs/configschema"
	"github.com/hashicorp/terraform/dag"
	"github.com/hashicorp/terraform/lang"
	"github.com/hashicorp/terraform/plans"
	"github.com/hashicorp/terraform/terraform"
	"github.com/hashicorp/terraform/tfdiags"
)

// IndexCommand is a Command implementation that emits crossreference data for Terraform files.
type IndexCommand struct {
	Meta
}

func (c *IndexCommand) Run(args []string) int {
	// Note: mostly adapted from graph.go.
	var drawCycles bool
	var graphTypeStr string
	var moduleDepth int
	var verbose bool

	args = c.Meta.process(args)
	cmdFlags := c.Meta.defaultFlagSet("graph")
	cmdFlags.BoolVar(&drawCycles, "draw-cycles", false, "draw-cycles")
	cmdFlags.StringVar(&graphTypeStr, "type", "", "type")
	cmdFlags.IntVar(&moduleDepth, "module-depth", -1, "module-depth")
	cmdFlags.BoolVar(&verbose, "verbose", false, "verbose")
	cmdFlags.Usage = func() { c.Ui.Error(c.Help()) }
	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing command-line flags: %s\n", err.Error()))
		return 1
	}

	configPath, err := ModulePath(cmdFlags.Args())
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}

	// Check for user-supplied plugin path
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		c.Ui.Error(fmt.Sprintf("Error loading plugin path: %s", err))
		return 1
	}

	// Check if the path is a plan
	var plan *plans.Plan
	planFile, err := c.PlanFile(configPath)
	if err != nil {
		c.Ui.Error(err.Error())
		return 1
	}
	if planFile != nil {
		// Reset for backend loading
		configPath = ""
	}

	var diags tfdiags.Diagnostics

	backendConfig, backendDiags := c.loadBackendConfig(configPath)
	diags = diags.Append(backendDiags)
	if diags.HasErrors() {
		c.showDiagnostics(diags)
		return 1
	}

	// Load the backend
	b, backendDiags := c.Backend(&BackendOpts{
		Config: backendConfig,
	})
	diags = diags.Append(backendDiags)
	if backendDiags.HasErrors() {
		c.showDiagnostics(diags)
		return 1
	}

	// We require a local backend
	local, ok := b.(backend.Local)
	if !ok {
		c.showDiagnostics(diags) // in case of any warnings in here
		c.Ui.Error(ErrUnsupportedLocalOp)
		return 1
	}

	// This is a read-only command
	c.ignoreRemoteBackendVersionConflict(b)

	// Build the operation
	opReq := c.Operation(b)
	opReq.ConfigDir = configPath
	opReq.ConfigLoader, err = c.initConfigLoader()
	opReq.PlanFile = planFile
	opReq.AllowUnsetVariables = true
	if err != nil {
		diags = diags.Append(err)
		c.showDiagnostics(diags)
		return 1
	}

	// Get the context
	ctx, _, ctxDiags := local.Context(opReq)
	diags = diags.Append(ctxDiags)
	if ctxDiags.HasErrors() {
		c.showDiagnostics(diags)
		return 1
	}

	// Determine the graph type
	graphType := terraform.GraphTypePlan
	if plan != nil {
		graphType = terraform.GraphTypeApply
	}

	if graphTypeStr != "" {
		v, ok := terraform.GraphTypeMap[graphTypeStr]
		if !ok {
			c.Ui.Error(fmt.Sprintf("Invalid graph type requested: %s", graphTypeStr))
			return 1
		}

		graphType = v
	}

	// Skip validation during graph generation - we want to see the graph even if
	// it is invalid for some reason.
	g, graphDiags := ctx.Graph(graphType, &terraform.ContextGraphOpts{
		Verbose:  verbose,
		Validate: false,
	})

	diags = diags.Append(graphDiags)
	if graphDiags.HasErrors() {
		c.showDiagnostics(diags)
		return 1
	}

	// Copy-pasted until here from graph.

	indexDiags := c.index(g)
	diags = diags.Append(indexDiags)

	return c.showResults(diags /*jsonOutput*/, true)
}

func (c *IndexCommand) index(g *terraform.Graph) tfdiags.Diagnostics {
	// Note: execute the binary with TF_LOG=true env var to see logs.
	// Otherwise they are only dumped to crash.log on crash.
	log.Printf("[INFO] Index starting")
	var diags tfdiags.Diagnostics

	var mu sync.Mutex

	g.AcyclicGraph.Walk(func(v dag.Vertex) tfdiags.Diagnostics {
		// Lock so our log output is not messed.. could remove later or restrict to sensitive parts
		mu.Lock()
		defer mu.Unlock()

		log.Printf("[INFO] Index walking %v of type '%s'", v, reflect.TypeOf(v))

		if refable, isRefable := v.(terraform.GraphNodeReferenceable); isRefable {
			log.Printf("[INFO] Got a Referenceable in ModulePath:'%+v'", refable.ModulePath())

			// HACK: Need this explicit check, since the root module, event thought Referenceable, will panic when we try to get a refable address
			// (why doesn't it just return empty list, just like the doc says?).
			isTheRootModule := reflect.TypeOf(v).String() == "*terraform.nodeCloseModule" && refable.ModulePath().IsRoot()
			if !isTheRootModule {
				// Relative to ModulePath:
				log.Printf("[INFO] .. ReferenceableAddrs:[%+v]", refable.ReferenceableAddrs())
			}
			if !refable.ModulePath().IsRoot() {
				log.Printf("[INFO] ..non-root, ancestors %+v", refable.ModulePath().Ancestors())
				callerModule, call := refable.ModulePath().Call()
				log.Printf("[INFO] ..(the containing module is called from module '%+v', with address '%+v')", callerModule, call)
			}
		}

		if refer, isRefer := v.(terraform.GraphNodeReferencer); isRefer {
			log.Printf("[INFO] Is a Referencer, refs:")
			for _, r := range refer.References() {
				log.Printf("[INFO] ... subject '%v', sourceRange '%+v', traversalRange '%+v",
					r.Subject, r.SourceRange, r.Remaining.SourceRange())
				for _, t := range r.Remaining {
					log.Printf("[INFO]   ... trav srcRange '%v' type '%s'", t.SourceRange(), reflect.TypeOf(t))
				}
			}
		}

		var d tfdiags.Diagnostics
		return d
	})

	return diags
}

func (c *IndexCommand) dumpModuleRefs(tfCtx *terraform.Context, cfg *configs.Config) {
	// See transform_module_variable.go for inspiration.
	_, call := cfg.Path.Call()
	moduleCall, exists := cfg.Parent.Module.ModuleCalls[call.Name]
	if !exists {
		// Should not happen
		panic(fmt.Errorf("no module call block found for %s", cfg.Path))
	}
	// Artificial schema
	hclSchema := &hcl.BodySchema{}
	for _, v := range cfg.Module.Variables {
		hclSchema.Attributes = append(hclSchema.Attributes, hcl.AttributeSchema{
			Name:     v.Name,
			Required: v.Default == cty.NilVal,
		})
	}

	schema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{},
	}
	for _, v := range cfg.Module.Variables {
		schema.Attributes[v.Name] = &configschema.Attribute{
			Required: v.Default == cty.NilVal, // ?
			Type:     v.Type,
		}
	}

	ct, ctDiags := moduleCall.Config.Content(hclSchema)
	if ctDiags.HasErrors() {
		log.Printf("[INFO] Error while getting content from module call for %s", cfg.Path)
	} else {
		for _, v := range cfg.Module.Variables {
			if cta, exists := ct.Attributes[v.Name]; exists {
				log.Printf("[INFO] Content-att %v at %v / %v", cta.Name, cta.Range, cta.NameRange)
			}
		}
	}

	refs, diags := lang.ReferencesInBlock(moduleCall.Config, schema)
	if diags.HasErrors() {
		log.Printf("[INFO] Error while getting references from module call for %s", cfg.Path)
	} else {
		for _, r := range refs {
			log.Printf("[INFO] Ref: %+v", r)
		}
	}
	// TODO count, for_each

	// Resource refs
	// See backend/local/backend_plan.go for inspiration.
	//
	// NOTE: we should extract refs for top-level resources only, not from
	// child modules. Here we get from a child module just to experiment.
	for rn, r := range cfg.Module.ManagedResources {
		log.Printf("[INFO] Resource: %v -> %+v", rn, r)
		s := tfCtx.Schemas().ProviderSchema(r.Provider)
		if s == nil {
			panic(fmt.Errorf("no schema for %s", r.Provider))
		}
		rSchema, _ := s.SchemaForResourceAddr(r.Addr())
		if rSchema == nil {
			panic(fmt.Errorf("no schema for resource %s with addr %v", r.Provider, r.Addr()))
		}
		log.Printf("[INFO] Schema: %+v", rSchema)
		// TODO pass Config through the Content like above?
		rRefs, rDiags := lang.ReferencesInBlock(r.Config, rSchema)
		if rDiags.HasErrors() {
			log.Printf("[INFO] Error while getting references from resource %s", r.Addr())
		} else {
			for _, r := range rRefs {
				log.Printf("[INFO] ResRef: %+v", r)
			}
		}

	}
	// TODO count, for_each
}

func (c *IndexCommand) showResults(diags tfdiags.Diagnostics, jsonOutput bool) int {
	switch {
	case jsonOutput:
		c.Ui.Output("I'm json trust me")

	default:
		if len(diags) == 0 {
			c.Ui.Output(c.Colorize().Color("[green][bold]Success![reset]\n"))
		} else {
			c.showDiagnostics(diags)

			if !diags.HasErrors() {
				c.Ui.Output(c.Colorize().Color("[green][bold]Success![reset] But there were some validation warnings as shown above.\n"))
			}
		}
	}

	if diags.HasErrors() {
		return 1
	}
	return 0
}

func (c *IndexCommand) Synopsis() string {
	return "Check whether the configuration is valid"
}

func (c *IndexCommand) Help() string {
	helpText := `
Usage: terraform validate [options] [dir]

  Validate the configuration files in a directory, referring only to the
  configuration and not accessing any remote services such as remote state,
  provider APIs, etc.

  Validate runs checks that verify whether a configuration is syntactically
  valid and internally consistent, regardless of any provided variables or
  existing state. It is thus primarily useful for general verification of
  reusable modules, including correctness of attribute names and value types.

  It is safe to run this command automatically, for example as a post-save
  check in a text editor or as a test step for a re-usable module in a CI
  system.

  Validation requires an initialized working directory with any referenced
  plugins and modules installed. To initialize a working directory for
  validation without accessing any configured remote backend, use:
      terraform init -backend=false

  If dir is not specified, then the current directory will be used.

  To verify configuration in the context of a particular run (a particular
  target workspace, input variable values, etc), use the 'terraform plan'
  command instead, which includes an implied validation check.

Options:

  -json        Produce output in a machine-readable JSON format, suitable for
               use in text editor integrations and other automated systems.
               Always disables color.

  -no-color    If specified, output won't contain any color.
`
	return strings.TrimSpace(helpText)
}
