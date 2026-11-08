package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportImportRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := tempApp(t)
	ch, err := (Channels{A: src}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := (LogicalModels{A: src}).Create("my-code", "", ModelMeta{},
		[]TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (AccessConfigs{A: src}).Create("claude", "工作", lm.ID, "{}"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	sum, err := (Gateway{A: src}).ExportConfig(path, "")
	if err != nil || sum.Channels != 1 || sum.LogicalModels != 1 || sum.AccessConfigs != 1 {
		t.Fatalf("export = %+v, %v", sum, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "sk-secret") {
		t.Fatal("export must not contain channel key")
	}

	dst := tempApp(t)
	pre, err := (Gateway{A: dst}).PreviewImport(path, "")
	if err != nil || pre.Channels != 1 || len(pre.Conflicts) != 0 {
		t.Fatalf("preview = %+v, %v", pre, err)
	}
	res, err := (Gateway{A: dst}).ApplyImport(path, "")
	if err != nil || res.AddedChannels != 1 || res.AddedLogicalModels != 1 || res.AddedAccessConfigs != 1 {
		t.Fatalf("import = %+v, %v", res, err)
	}
	chs := (Channels{A: dst}).List()
	if len(chs) != 1 || chs[0].HasKey {
		t.Fatalf("imported channels = %+v", chs)
	}
	if got := (AccessConfigs{A: dst}).List("claude"); len(got) != 1 || got[0].IsCurrent {
		t.Fatalf("imported access = %+v", got)
	}

	// 再导入同一文件：全部冲突跳过，不重复创建。
	res2, err := (Gateway{A: dst}).ApplyImport(path, "")
	if err != nil || res2.AddedChannels != 0 || len(res2.Skipped) == 0 {
		t.Fatalf("reimport = %+v, %v", res2, err)
	}
}

// TestExportImportEncrypted 覆盖 C1/C2：口令导出为 v2 加密信封（明文不含
// 配置内容），无口令预览报 ErrEncryptedExport，错口令报「口令错误」，
// 正确口令预览/导入往返成立；v1 明文包不受影响。
func TestExportImportEncrypted(t *testing.T) {
	src := tempApp(t)
	ch, err := (Channels{A: src}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (LogicalModels{A: src}).Create("my-code", "", ModelMeta{},
		[]TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if _, err := (Gateway{A: src}).ExportConfig(path, "口令-pass-1"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"encrypted": true`) {
		t.Fatalf("应是加密信封：%s", b)
	}
	if strings.Contains(string(b), "主渠道") || strings.Contains(string(b), "sk-secret") {
		t.Fatalf("加密导出不应含明文配置：%s", b)
	}

	dst := tempApp(t)
	if _, err := (Gateway{A: dst}).PreviewImport(path, ""); !errors.Is(err, ErrEncryptedExport) {
		t.Fatalf("无口令预览应报 ErrEncryptedExport：%v", err)
	}
	if _, err := (Gateway{A: dst}).PreviewImport(path, "wrong"); err == nil || !strings.Contains(err.Error(), "口令错误") {
		t.Fatalf("错口令应报口令错误：%v", err)
	}
	pre, err := (Gateway{A: dst}).PreviewImport(path, "口令-pass-1")
	if err != nil || pre.Channels != 1 || pre.LogicalModels != 1 {
		t.Fatalf("正确口令预览 = %+v, %v", pre, err)
	}
	res, err := (Gateway{A: dst}).ApplyImport(path, "口令-pass-1")
	if err != nil || res.AddedChannels != 1 || res.AddedLogicalModels != 1 {
		t.Fatalf("正确口令导入 = %+v, %v", res, err)
	}
}
