package main

import (
	"strings"
	"testing"

	"veri/clikit/buildinfo"
	"veri/clikit/skill"
	"veri/internal/spec"
)

func TestRootEmbeddedAssetsIntegrity(t *testing.T) {
	meta, err := buildinfo.LoadConfig(embeddedAssets)
	if err != nil {
		t.Fatalf("LoadConfig from embeddedAssets failed: %v", err)
	}
	if meta.ProductName != spec.AppName || meta.Version == "" || meta.Version == buildinfo.DefaultDevVersion {
		t.Fatalf("unexpected embedded build/config.yml metadata: %+v", meta)
	}

	md, err := skill.Read(embeddedAssets, spec.SkillAssetPath)
	if err != nil {
		t.Fatalf("skill.Read from embeddedAssets failed: %v", err)
	}
	if !strings.Contains(md, "name: "+spec.AppName) {
		t.Fatalf("embedded SKILL.md missing frontmatter name")
	}
}
