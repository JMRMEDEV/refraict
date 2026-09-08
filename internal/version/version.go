// Package version exposes the build version, injectable at link time via
// -ldflags "-X github.com/refraict/refraict/internal/version.Version=vX.Y.Z".
// Defaults to "dev" for local/source builds.
package version

// Version is the build version. Overridden by the release workflow's ldflags;
// "dev" when built from source without the flag.
var Version = "dev"
