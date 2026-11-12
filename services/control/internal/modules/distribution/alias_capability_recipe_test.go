package distribution

import (
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestAliasContextCeilingRequiresRecognizedSource(t *testing.T) {
	catalog := agentwire.ModelCatalogDigest()
	derivedRecipe := agentwire.DerivedServingRecipeDigest()
	unknown := strings.Repeat("a", 64)
	for _, test := range []struct {
		name, kind, catalog string
		recipe              *string
		want                int
	}{
		{"approved base", "approved_base", catalog, nil, 1024},
		{"unknown base catalogue", "approved_base", unknown, nil, 0},
		{"unexpected base recipe", "approved_base", catalog, &unknown, 0},
		{"verified derived recipe", agentwire.DerivedModelKind, unknown, &derivedRecipe, 1024},
		{"unrecognized derived recipe", agentwire.DerivedModelKind, unknown, &unknown, 0},
		{"missing derived recipe", agentwire.DerivedModelKind, unknown, nil, 0},
		{"unrecognized kind", "unrecognized", catalog, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := aliasContextCeiling(test.kind, test.catalog, test.recipe, 1024); got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}
