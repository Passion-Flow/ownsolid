package main

import (
	"cmp"
	"slices"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

type notice struct {
	ID           string   `json:"id"`
	Path         string   `json:"path"`
	SHA256       string   `json:"sha256"`
	ReferencedBy []string `json:"referenced_by"`
}

type noticeInventory struct {
	ReleaseID      string   `json:"release_id"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Notices        []notice `json:"notices"`
}

func bundleNotices(result bundle.Result) noticeInventory {
	inventory := noticeInventory{ReleaseID: result.ReleaseID, ManifestSHA256: result.ManifestSHA256, Notices: []notice{}}
	references := map[string][]string{}
	for _, asset := range result.Manifest.Assets {
		for _, id := range asset.Licenses {
			references[id] = append(references[id], asset.ID)
		}
	}
	for _, asset := range result.Manifest.Assets {
		if asset.Kind != "license" {
			continue
		}
		uses := references[asset.ID]
		slices.Sort(uses)
		if uses == nil {
			uses = []string{}
		}
		inventory.Notices = append(inventory.Notices, notice{ID: asset.ID, Path: asset.Path, SHA256: asset.SHA256, ReferencedBy: uses})
	}
	slices.SortFunc(inventory.Notices, func(a, b notice) int { return cmp.Compare(a.ID, b.ID) })
	return inventory
}
