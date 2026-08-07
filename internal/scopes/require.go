package scopes

// Set is a small opaque-scope set, implemented as a map for O(1)
// membership — never as a sorted slice with a binary search that would
// tempt someone to add a comparator that inspects scope structure.
type Set map[string]struct{}

// NewSet builds a Set from a slice, deduplicating.
func NewSet(scopeList []string) Set {
	s := make(Set, len(scopeList))
	for _, sc := range scopeList {
		s[sc] = struct{}{}
	}
	return s
}

// Has reports exact, opaque membership — a byte-for-byte string compare,
// nothing else.
func (s Set) Has(scope string) bool {
	_, ok := s[scope]
	return ok
}

// HasAll reports whether every scope in required is present in s.
func (s Set) HasAll(required []string) bool {
	for _, r := range required {
		if !s.Has(r) {
			return false
		}
	}
	return true
}

// Missing returns every scope in required that s does not have, in
// required's original order. A nil/empty result means the requirement is
// already satisfied.
func (s Set) Missing(required []string) []string {
	var missing []string
	for _, r := range required {
		if !s.Has(r) {
			missing = append(missing, r)
		}
	}
	return missing
}
