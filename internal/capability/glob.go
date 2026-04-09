package capability

import "strings"

// Match reports whether a glob pattern matches an input path.
//
// Supported syntax (v0.1):
//   - "*" matches a single path segment (anything between slashes,
//     including the empty string)
//   - "**" matches zero or more path segments
//   - any other character is matched literally
//   - path segments are separated by "/"
//
// NOT supported in v0.1:
//   - brace expansion: "{a,b,c}" — added when an adapter needs it
//   - character classes: "[abc]"
//   - intra-segment wildcards: "foo*bar" (the "*" must be a whole segment)
//
// Match performs a full match against the entire input, not a prefix
// or substring match. An empty pattern matches only the empty input.
func Match(pattern, input string) bool {
	return matchSegments(splitPath(pattern), splitPath(input))
}

func splitPath(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegments(pat, in []string) bool {
	if len(pat) == 0 {
		return len(in) == 0
	}
	head := pat[0]
	if head == "**" {
		// "**" matches zero or more segments. Try each split point.
		for i := 0; i <= len(in); i++ {
			if matchSegments(pat[1:], in[i:]) {
				return true
			}
		}
		return false
	}
	if len(in) == 0 {
		return false
	}
	if head == "*" || head == in[0] {
		return matchSegments(pat[1:], in[1:])
	}
	return false
}
