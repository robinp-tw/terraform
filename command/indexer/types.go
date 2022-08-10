package indexer

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
