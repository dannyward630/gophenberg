// SPDX-License-Identifier: Apache-2.0

package themehost_test

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/gopherium/gophenberg/internal/themehost"
)

// dockerfilePath is the file the release image is built from.
const dockerfilePath = "../../Dockerfile"

// nodeImage matches a distroless Node.js image the Dockerfile names, holding its major.
var nodeImage = regexp.MustCompile(`distroless/nodejs(\d+)`)

func TestTheImageShipsTheOldestNodeMajor(t *testing.T) {
	t.Parallel()

	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("reading the Dockerfile: %v", err)
	}

	named := nodeImage.FindAllSubmatch(dockerfile, -1)
	if len(named) == 0 {
		t.Fatalf("the Dockerfile names no distroless Node.js image, want nodejs%d", themehost.OldestNodeMajor)
	}
	want := strconv.Itoa(themehost.OldestNodeMajor)
	for _, image := range named {
		if major := string(image[1]); major != want {
			t.Errorf("the Dockerfile names distroless/nodejs%s, want nodejs%s, the major about names", major, want)
		}
	}
}
