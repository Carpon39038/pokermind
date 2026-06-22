package store

import "testing"

func TestProvidersCRUD(t *testing.T) {
	rec := freshStore(t)

	// List 空
	got, err := rec.ListProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("initial list = %d want 0", len(got))
	}

	// Upsert 新增
	p, err := rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com", "sk-abc")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "deepseek" || p.Kind != "openai" || p.APIKey != "sk-abc" {
		t.Errorf("upsert returned wrong: %+v", p)
	}

	// GetByName
	g, err := rec.GetProviderByName("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if g.APIKey != "sk-abc" {
		t.Errorf("getbyname apikey = %q", g.APIKey)
	}

	// Upsert 已存在:空 apiKey 不覆盖
	_, err = rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com/v2", "")
	if err != nil {
		t.Fatal(err)
	}
	g, _ = rec.GetProviderByName("deepseek")
	if g.APIKey != "sk-abc" {
		t.Errorf("empty apiKey should not overwrite; got %q", g.APIKey)
	}
	if g.BaseURL != "https://api.deepseek.com/v2" {
		t.Errorf("base_url not updated; got %q", g.BaseURL)
	}

	// Upsert 已存在:非空 apiKey 覆盖
	_, _ = rec.UpsertProvider("deepseek", "openai", "https://api.deepseek.com/v2", "sk-new")
	g, _ = rec.GetProviderByName("deepseek")
	if g.APIKey != "sk-new" {
		t.Errorf("apiKey should be overwritten; got %q", g.APIKey)
	}

	// List 含一条
	got, _ = rec.ListProviders()
	if len(got) != 1 {
		t.Errorf("list len = %d want 1", len(got))
	}

	// Delete
	if err := rec.DeleteProvider("deepseek"); err != nil {
		t.Fatal(err)
	}
	got, _ = rec.ListProviders()
	if len(got) != 0 {
		t.Errorf("after delete list = %d want 0", len(got))
	}

	// Delete 不存在的应报错
	if err := rec.DeleteProvider("nonexistent"); err == nil {
		t.Error("delete nonexistent should error")
	}
}
