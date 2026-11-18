package aws

import (
	"strings"
	"testing"

	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

// TestLoadConfigAcceptsAnyRegion: the Region is not checked against a list, so a Region added after a release, or a
// private endpoint's Region, is accepted.
func TestLoadConfigAcceptsAnyRegion(t *testing.T) {
	var p FargateProvider
	err := p.loadConfig(strings.NewReader(`
Region = "example-region-1"
Subnets = ["subnet-12345678"]
`))
	assert.NilError(t, err)
	assert.Check(t, is.Equal(p.region, "example-region-1"))
}

// TestLoadConfigRegionIsOptional: with no Region in the file, the AWS SDK resolves it later.
func TestLoadConfigRegionIsOptional(t *testing.T) {
	var p FargateProvider
	err := p.loadConfig(strings.NewReader(`
Subnets = ["subnet-12345678"]
`))
	assert.NilError(t, err)
	assert.Check(t, is.Equal(p.region, ""))
}
