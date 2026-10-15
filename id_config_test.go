package mongoplus_test

import (
	"encoding/json"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"gopkg.in/yaml.v3"
)

func TestIDConfigConstantsAndDefaults(t *testing.T) {
	for _, item := range []struct {
		name   string
		config mp.IDConfig
		want   mp.IDConfig
	}{
		{"default", mp.IDConfig{}, mp.IDConfig{Type: mp.IDTypeObjectID, Generation: mp.IDGenerationAuto}},
		{"int64 default", mp.IDConfig{Type: mp.IDTypeInt64}, mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationIncrement}},
		{"string default", mp.IDConfig{Type: mp.IDTypeString}, mp.IDConfig{Type: mp.IDTypeString, Generation: mp.IDGenerationManual}},
		{"manual objectId", mp.IDConfig{Type: mp.IDTypeObjectID, Generation: mp.IDGenerationManual}, mp.IDConfig{Type: mp.IDTypeObjectID, Generation: mp.IDGenerationManual}},
		{"manual int64", mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationManual}, mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationManual}},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := item.config.Normalize()
			if err != nil || got != item.want {
				t.Fatalf("主键常量或默认值不正确: %+v %v", got, err)
			}
		})
	}
	for _, config := range []mp.IDConfig{
		{Type: mp.IDType("unknown")},
		{Type: mp.IDTypeInt64, BlockSize: -1},
		{Type: mp.IDTypeInt64, BlockSize: 1_000_001},
		{Type: mp.IDTypeString, BlockSize: 10},
		{Type: mp.IDTypeInt64, Generation: mp.IDGeneration("unknown")},
		{Type: mp.IDTypeString, Generation: mp.IDGenerationAuto},
		{Type: mp.IDTypeObjectID, Generation: mp.IDGenerationIncrement},
	} {
		if _, err := config.Normalize(); err == nil {
			t.Fatalf("允许非法主键类型或生成组合: %+v", config)
		}
	}
}

func TestIDConfigStringSerialization(t *testing.T) {
	want := mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationIncrement}
	data, err := json.Marshal(want)
	if err != nil || string(data) != `{"type":"int64","generation":"increment"}` {
		t.Fatalf("JSON 配置格式发生变化: %s %v", data, err)
	}
	var fromJSON mp.IDConfig
	if err := json.Unmarshal(data, &fromJSON); err != nil || fromJSON != want {
		t.Fatalf("JSON 常量解析失败: %+v %v", fromJSON, err)
	}
	data, err = yaml.Marshal(want)
	if err != nil || string(data) != "type: int64\ngeneration: increment\n" {
		t.Fatalf("YAML 配置格式发生变化: %s %v", data, err)
	}
	var fromYAML mp.IDConfig
	if err := yaml.Unmarshal(data, &fromYAML); err != nil || fromYAML != want {
		t.Fatalf("YAML 常量解析失败: %+v %v", fromYAML, err)
	}
}
