// Package version holds the build-time version of the soulstream-archivist binary.
package version

// Version is "dev" for source builds; release builds overwrite it via
//
//	-ldflags "-X github.com/impire-io/soulstream-archivist/internal/version.Version=<semver>"
var Version = "dev"
