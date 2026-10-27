package bridge

import (
	"strings"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

func logicalSvc(t *testing.T) LogicalModels {
	t.Helper()
	return LogicalModels{A: tempApp(t)}
}

// saveTestChannel 直接落库一条可用渠道（测试不触碰密钥存储）。
func saveTestChannel(t *testing.T, a *app.App, name string) storage.Channel {
	t.Helper()
	c, err := a.Store().Channels().Save(storage.Channel{
		Name: name, Protocol: "anthropic", BaseURL: "https://api.example.com",
		CredentialRef: "channel:test-" + name, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCreateModelWithOrderedTargets：Create 两目标，List 顺序与 ChannelName 正确。
func TestLogicalCreateModelWithOrderedTargets(t *testing.T) {
	s := logicalSvc(t)
	cha := saveTestChannel(t, s.A, "alpha")
	chb := saveTestChannel(t, s.A, "beta")
	m, err := s.Create("my-code", "编码模型", ModelMeta{}, []TargetSpec{
		{ChannelID: cha.ID, ModelName: "m1", Priority: 0},
		{ChannelID: chb.ID, ModelName: "m2", Priority: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "my-code" || m.Description != "编码模型" || !m.Enabled {
		t.Fatalf("model view: %+v", m)
	}
	if len(m.Targets) != 2 {
		t.Fatalf("targets: %+v", m.Targets)
	}
	if m.Targets[0].ChannelName != "alpha" || m.Targets[0].ModelName != "m1" ||
		m.Targets[1].ChannelName != "beta" || m.Targets[1].ModelName != "m2" {
		t.Fatalf("target order/names: %+v", m.Targets)
	}
	if !m.Targets[0].Healthy || !m.Targets[1].Healthy {
		t.Fatalf("healthy targets expected: %+v", m.Targets)
	}
	list := s.List()
	if len(list) != 1 || len(list[0].Targets) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if list[0].Targets[0].ChannelName != "alpha" || list[0].Targets[1].ChannelName != "beta" {
		t.Fatalf("list target order: %+v", list[0].Targets)
	}
}

// TestCreateRejectsBadName：名字含空格（不匹配 ^[a-zA-Z0-9._-]+$）报错。
func TestLogicalCreateRejectsBadName(t *testing.T) {
	s := logicalSvc(t)
	ch := saveTestChannel(t, s.A, "alpha")
	tgts := []TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}}
	if _, err := s.Create("my model", "", ModelMeta{}, tgts); err == nil {
		t.Fatal("name with space must be rejected")
	}
	if _, err := s.Create("", "", ModelMeta{}, tgts); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := s.Create("ok.name_1-2", "", ModelMeta{}, nil); err == nil {
		t.Fatal("empty targets must be rejected on create")
	}
}

// TestUpdateReplacesTargets：Update 全量替换目标（DeleteByModel + 重插）。
func TestLogicalUpdateReplacesTargets(t *testing.T) {
	s := logicalSvc(t)
	cha := saveTestChannel(t, s.A, "alpha")
	chb := saveTestChannel(t, s.A, "beta")
	m, err := s.Create("my-code", "d", ModelMeta{}, []TargetSpec{
		{ChannelID: cha.ID, ModelName: "m1", Priority: 0},
		{ChannelID: chb.ID, ModelName: "m2", Priority: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(m.ID, "my-code", "d2", ModelMeta{}, false, []TargetSpec{
		{ChannelID: chb.ID, ModelName: "only", Priority: 0},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "d2" || got.Enabled {
		t.Fatalf("fields: %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].ModelName != "only" || got.Targets[0].ChannelName != "beta" {
		t.Fatalf("targets must be replaced: %+v", got.Targets)
	}
}

func TestLogicalModelMetadataValidation(t *testing.T) {
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low", "bogus"}}); err == nil {
		t.Fatal("未知思考等级应报错")
	}
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low"}, DefaultReasoningLevel: "high"}); err == nil {
		t.Fatal("默认等级不在列表应报错")
	}
	if err := validateModelMeta(ModelMeta{ContextWindow: -1}); err == nil {
		t.Fatal("负上下文窗口应报错")
	}
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high"}); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalModelCreatePersistsMeta(t *testing.T) {
	a := tempApp(t)
	ch, err := a.Store().Channels().Save(storage.Channel{
		Name: "c", Protocol: "anthropic", BaseURL: "http://u", CredentialRef: "ref",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := LogicalModels{A: a}
	got, err := svc.Create("my-code", "", ModelMeta{
		DisplayName: "My Code", ContextWindow: 1000000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
	}, []TargetSpec{{ChannelID: ch.ID, ModelName: "up"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Code" || got.ContextWindow != 1000000 ||
		len(got.ReasoningLevels) != 2 || got.DefaultReasoningLevel != "high" {
		t.Fatalf("%+v", got)
	}
}

// TestRemoveModel：删除后 List 无。
func TestLogicalRemoveModel(t *testing.T) {
	s := logicalSvc(t)
	ch := saveTestChannel(t, s.A, "alpha")
	m, err := s.Create("my-code", "", ModelMeta{}, []TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}})
	if err != nil {
		t.Fatal(err)
	}
	s.Remove(m.ID)
	if list := s.List(); len(list) != 0 {
		t.Fatalf("list after remove: %+v", list)
	}
	if _, err := s.Get(m.ID); err == nil {
		t.Fatal("get after remove must error")
	}
	// 空串/重复名也拒绝：UNIQUE 译友好错误
	if _, err := s.Create("my-code", "", ModelMeta{}, []TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("my-code", "", ModelMeta{}, []TargetSpec{{ChannelID: ch.ID, ModelName: "m2", Priority: 0}}); err == nil ||
		!strings.Contains(err.Error(), "已存在") {
		t.Fatalf("duplicate name: %v", err)
	}
}
