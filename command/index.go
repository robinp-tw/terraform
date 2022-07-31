package command

import (
	//	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/command/indexer"
	"github.com/hashicorp/terraform/terraform"
	"github.com/hashicorp/terraform/tfdiags"
)

// IndexCommand is a Command implementation that emits crossreference data for Terraform files.
type IndexCommand struct {
	Meta
}

func (c *IndexCommand) Run(args []string) int {
	// Note: mostly adapted from validate.go.
	args = c.Meta.process(args)

	jsonOutput := true
	cmdFlags := c.Meta.defaultFlagSet("index")
	//cmdFlags.BoolVar(&jsonOutput, "json", false, "produce JSON output")
	cmdFlags.Usage = func() { c.Ui.Error(c.Help()) }
	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing command-line flags: %s\n", err.Error()))
		return 1
	}

	var diags tfdiags.Diagnostics

	// After this point, we must only produce JSON output if JSON mode is
	// enabled, so all errors should be accumulated into diags and we'll
	// print out a suitable result at the end, depending on the format
	// selection. All returns from this point on must be tail-calls into
	// c.showResults in order to produce the expected output.
	args = cmdFlags.Args()

	var dirPath string
	if len(args) == 1 {
		dirPath = args[0]
	} else {
		dirPath = "."
	}
	dir, err := filepath.Abs(dirPath)
	if err != nil {
		diags = diags.Append(fmt.Errorf("unable to locate module: %s", err))
		return c.showResults(diags, jsonOutput)
	}

	// Check for user-supplied plugin path
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(fmt.Errorf("error loading plugin path: %s", err))
		return c.showResults(diags, jsonOutput)
	}

	indexDiags := c.index(dir)
	diags = diags.Append(indexDiags)

	return c.showResults(diags, jsonOutput)
}

func (c *IndexCommand) index(dir string) tfdiags.Diagnostics {
	// Note: execute the binary with TF_LOG=true env var to see logs.
	// Otherwise they are only dumped to crash.log on crash.
	log.Printf("[INFO] Index starting")
	var diags tfdiags.Diagnostics

	cfg, cfgDiags := c.loadConfig(dir)
	diags = diags.Append(cfgDiags)

	if diags.HasErrors() {
		return diags
	}

	// TODO(indexing): index input references, maybe from values files, if any?

	// Note: below is leftover from validate.go. If we don't need it, remove.
	//
	// "validate" is to check if the given module is valid regardless of
	// input values, current state, etc. Therefore we populate all of the
	// input values with unknown values of the expected type, allowing us
	// to perform a type check without assuming any particular values.
	varValues := make(terraform.InputValues)
	for name, variable := range cfg.Module.Variables {
		ty := variable.Type
		if ty == cty.NilType {
			// Can't predict the type at all, so we'll just mark it as
			// cty.DynamicVal (unknown value of cty.DynamicPseudoType).
			ty = cty.DynamicPseudoType
		}
		varValues[name] = &terraform.InputValue{
			Value:      cty.UnknownVal(ty),
			SourceType: terraform.ValueFromCLIArg,
		}
	}

	opts, err := c.contextOpts()
	if err != nil {
		diags = diags.Append(err)
		return diags
	}
	opts.Config = cfg
	opts.Variables = varValues

	tfCtx, ctxDiags := terraform.NewContext(opts)
	diags = diags.Append(ctxDiags)
	if ctxDiags.HasErrors() {
		return diags
	}

	ixer := indexer.NewIndexer()
	ixer.RecursivelyIndexModules(tfCtx, cfg)

	return diags
}

func (c *IndexCommand) showResults(diags tfdiags.Diagnostics, jsonOutput bool) int {
	switch {
	case jsonOutput:
		// For now we stream out json directly. So let's not output anything.
		//c.Ui.Output("{\"value\": \"I'm json trust me\"}")

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
