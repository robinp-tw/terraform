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

// CanonicalModuleIdentity is a universal, normalized identity for a given Terraform module.
// For example, in git-stored modules, this can be the repo + path, together with a branch
// reference (though a commit hash is the most universal, and in fact should be used.. but
// that might need some nontrivial lookup.. or should we defer canonical mapping to some
// later stage?)
//
// Ok, see ModuleIdentity below then.
type CanonicalModuleIdentity struct {
}

// ModuleIdentity is a good-enough, though maybe not exact or universal module identity.
// For example, a given module might be referred to by local path (from other modules in
// the same repo), gitty ref, or a direct http-based artifact reference.
//
// To be fleshed out. Might need some extra data like git module of referencer etc, so
// there's better chance of canonicalizing later.
type ModuleIdentity struct {
	SourceReference string
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

}

func somethingAboutModuleCalls(cfg *configs.Config) {
	// Stashing some code here. It goes upwards, from current module to caller,
	// which doesn't make sense, but anyway.
	_, call := cfg.Path.Call()
	moduleCall, exists := cfg.Parent.Module.ModuleCalls[call.Name]
	if !exists {
		// Should not happen
		panic(fmt.Errorf("no module call block found for %s", cfg.Path))
	}

	// From here it can be generic.

	hclSchema, schema := mkSyntheticSchema(cfg)

	callContent, callContentDiags := moduleCall.Config.Content(hclSchema)
	if callContentDiags.HasErrors() {
		log.Printf("[INFO] Error while getting content from module call for %s", cfg.Path)
	} else {
		for _, v := range cfg.Module.Variables {
			if cta, exists := callContent.Attributes[v.Name]; exists {
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
