package indexer

import (
	"fmt"
	"log"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/configs/configschema"
	"github.com/hashicorp/terraform/lang"
	"github.com/hashicorp/terraform/terraform"
	"github.com/zclconf/go-cty/cty"
)

type Indexer struct {
	whatever bool
	emitter
}

func NewIndexer() *Indexer {
	return &Indexer{
		whatever: true,
		emitter:  &someEmitter{},
	}
}

type someEmitter struct{}

func (se *someEmitter) EmitModuleIdentity(mi ModuleIdentity) {
	// Random hack
	fmt.Printf("{\"moduleIdentity\": \"%s\"}", mi.SourceReference)
}

func (ixer *Indexer) RecursivelyIndexModules(tfCtx *terraform.Context, cfg *configs.Config) {

	// Notes:
	//
	// Definitions:
	// - config's config.go and module.go is a great source for traversing the tree and finding
	//   both definitions, and if they have RHS-es, then references from them.
	//
	// References:
	// - lang's references.go given function to get references from hcl.Expression and a hcl.Body
	//
	// File contents:
	// - parser has private methods to get files from a given directory. We can either expose and use those,
	//   or just gather the files from the hcl.Ranges encountered on module definitions.

	isRoot := cfg.Root == cfg
	var identity ModuleIdentity
	if isRoot {
		identity = ModuleIdentity{SourceReference: "the-root"}
	} else {
		identity = ModuleIdentity{SourceReference: cfg.SourceAddr}
	}
	ixer.EmitModuleIdentity(identity)

	mAddr := cfg.Path

	for _, v := range cfg.Module.Variables {
		// Note: this prints the top-level variables definitions, but would be nice if we could also get hold of the deeper variables, in case
		// the variable type is an object. Might need some HCL-y AST mining though.. also only useful if we can actually connect up
		// the references from use-sites.

		// Also see below in module call indexing, by default we can't get hold of the references to these, so emitting the
		// definitions is pretty useless without that (but probably can be worked out, see comment there).
		log.Printf("Var name='%s' in module '%s' in range '%v'", v.Name, mAddr, v.DeclRange)
	}

	for _, l := range cfg.Module.Locals {
		log.Printf("Local name='%s' in module '%s' in range '%v'", l.Name, mAddr, l.DeclRange)
		refs, _ := lang.ReferencesInExpr(l.Expr)
		for _, ref := range refs {
			log.Printf("Ref in local '%s' referencing '%s' from range '%v'", l.Name, ref.Subject.String(), ref.SourceRange)
		}
	}

	for _, o := range cfg.Module.Outputs {
		log.Printf("Output name='%s' in module '%s' in range '%v'", o.Name, mAddr, o.DeclRange)
		refs, _ := lang.ReferencesInExpr(o.Expr)
		for _, ref := range refs {
			log.Printf("Ref in output '%s' referencing '%s' from range '%v'", o.Name, ref.Subject.String(), ref.SourceRange)
		}
	}

	for key, call := range cfg.Module.ModuleCalls {
		log.Printf("ModuleCall in '%s', key '%s', name '%s', sourceAddr '%s', sourceAddrRange '%v', declRange '%v'", mAddr, key, call.Name, call.SourceAddr, call.SourceAddrRange, call.DeclRange)
		somethingAboutModuleCalls(call, cfg.Children[key])
	}

	for _, childCfg := range cfg.Children {
		ixer.RecursivelyIndexModules(tfCtx, childCfg)
	}

}

func somethingAboutModuleCalls(moduleCall *configs.ModuleCall, calledConfig *configs.Config) {
	hclSchema, schema := mkSyntheticSchema(calledConfig)

	callContent, callContentDiags := moduleCall.Config.Content(hclSchema)
	if callContentDiags.HasErrors() {
		log.Printf("[INFO] Error while getting content from module call for %s", calledConfig.Path)
	} else {
		for _, v := range calledConfig.Module.Variables {
			if cta, exists := callContent.Attributes[v.Name]; exists {
				log.Printf("[INFO] Content-att %v at %v / %v", cta.Name, cta.Range, cta.NameRange)
			}
		}
	}

	// These are the refs from the expressions passed to the various parameters.

	// We should also emit references to the actual variable definitions.
	// Maybe blocktoattr.ExpandedVariables can serve as some inspiration?
	refs, diags := lang.ReferencesInBlock(moduleCall.Config, schema)
	if diags.HasErrors() {
		log.Printf("[INFO] Error while getting references from module call for %s", calledConfig.Path)
	} else {
		for _, r := range refs {
			log.Printf("[INFO] ModuleCall Ref: %+v", r)
		}
	}
}

// Why do we need to synthetize the schema from the config block? Can't we access some
// prepared schema somehow?
//
// See transform_module_variable.go for the inspiration.
func mkSyntheticSchema(cfg *configs.Config) (*hcl.BodySchema, *configschema.Block) {
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
			Required: v.Default == cty.NilVal,
			Type:     v.Type,
		}
	}

	return hclSchema, schema
}

func somethingWithResources(tfCtx *terraform.Context, cfg *configs.Config) {
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
}
