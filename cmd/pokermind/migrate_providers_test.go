package main

import (
	"os"
	"path/filepath"
	"testing"

	"pokermind/internal/store"
)

func TestMigrateProvidersFromEnv(t *testing.T) {
	t.Setenv("POKERMIND_DEEPSEEK_API_KEY", "sk-deepseek-xxx")
	t.Setenv("POKERMIND_DEEPSEEK_BASE_URL", "https://api.deepseek.com")
	t.Setenv("POKERMIND_GLM_API_KEY", "sk-glm-yyy")

	rec, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}

	ds, _ := rec.GetProviderByName("deepseek")
	if ds == nil || ds.APIKey != "sk-deepseek-xxx" || ds.Kind != "openai" {
		t.Errorf("deepseek not migrated: %+v", ds)
	}
	glm, _ := rec.GetProviderByName("glm")
	if glm == nil || glm.APIKey != "sk-glm-yyy" || glm.Kind != "openai" {
		t.Errorf("glm not migrated: %+v", glm)
	}

	// 幂等:再跑一遍不应覆盖
	t.Setenv("POKERMIND_DEEPSEEK_API_KEY", "sk-different")
	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}
	ds, _ = rec.GetProviderByName("deepseek")
	if ds.APIKey != "sk-deepseek-xxx" {
		t.Errorf("migrate should not overwrite; got %q", ds.APIKey)
	}
}

func TestMigrateProvidersFromEnv_NoEnv(t *testing.T) {
	os.Unsetenv("POKERMIND_DEEPSEEK_API_KEY")
	os.Unsetenv("POKERMIND_GLM_API_KEY")
	rec, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	if err := migrateProvidersFromEnv(rec); err != nil {
		t.Fatal(err)
	}
	list, _ := rec.ListProviders()
	if len(list) != 0 {
		t.Errorf("no env should give no providers, got %d", len(list))
	}
}
