package indexer

// Uniq is a unique value representing some source-defined entity.
// For example, if a new struct instance is created, that should have a unique identity.
// Usually the identity is related to the source code location where a name for that instance
// is bound.
//
// Ideally the Uniq unambiguously identifies the repo+commit+file-path+source-location+identity of the entity,
// though it is hard to tell upfront, as it depends on the full indexing pipeline what makes sense.
// For example, for a monorepo, repo+commit can be omitted, since everything is anyway indexed at a consistent snapshot.
//
// An other, somewhat contradicting requirement for a Uniq, is that it should be constructable at the reference-site too,
// so we can actually emit the reference pointing to the Uniq. But for example, the file-path of where the referred
// entity is defined is rarely available at reference-sites AST, so it is better to rely on some other attributes.
// For example a module address, in case of Terraform, but really depends.
//
// Later we might utilize Glean to connect up references across different compilation units based on multiple metadata facets,
// but in the mean time we rely on the single Uniq value to be precise enough.
//
// (Note: in Kythe, the vnames.json entity renaming mechanism served as rewriting the uniq-s to more globally unambiguous ones)
type Uniq interface {
	UniqString() string
}

type emitter interface {
	EmitModuleIdentity(ModuleIdentity)
	// Below is a bare-minimum for crossreferencing.
	//
	// Later we can enrich the emitted metadata with all kind of goodies, for example
	// about the type of what is being defined or other details.

	// The actual source code of some file.
	// For now path is unique enough, but eventually we might want to put a Uniq-ish value here, also in
	// the hcl.Range Filename's.
	// EmitSource(content []byte, path string)
	// // A given unique is defined in source code at that given location.
	// EmitDefinition(hcl.Range, Uniq)
	// // Reference to some Uniq, from the given source location.
	// EmitReference(hcl.Range, Uniq)
}
