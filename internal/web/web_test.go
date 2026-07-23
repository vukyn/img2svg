package web

import (
	"io/fs"
	"testing"
)

// TestEmbedRoot verifies the embed FS is rooted at dist so the server's asset
// middleware finds files at the expected paths. It tolerates a build that ran
// only the .gitkeep placeholder (fresh checkout, no build-web): it asserts the
// FS opens and, when a real bundle is present, that index.html + assets resolve.
func TestEmbedRoot(t *testing.T) {
	root := FS()

	// The placeholder is always embedded; its presence proves the dist root resolves.
	if _, err := fs.Stat(root, ".gitkeep"); err != nil {
		t.Fatalf("embed root must contain the dist placeholder: %v", err)
	}

	// When a real build is embedded, the entrypoint + assets must be reachable.
	if _, err := fs.Stat(root, "index.html"); err == nil {
		if _, err := fs.Stat(root, "assets"); err != nil {
			t.Fatalf("a built bundle must expose assets/: %v", err)
		}
	}
}
