package sieve

import (
	"errors"
	"strings"
	"testing"

	"mailhearth/internal/model"
)

func TestMultiProviderSieveExtensionRequirements(t *testing.T) {
	cases := []struct {
		action    model.SieveAction
		condition model.SieveCondition
		extension string
	}{
		{model.SieveAction{Type: "move", Folder: "Archive"}, model.SieveCondition{Field: "subject", Value: "notice"}, "fileinto"},
		{model.SieveAction{Type: "flag"}, model.SieveCondition{Field: "subject", Value: "notice"}, "imap4flags"},
		{model.SieveAction{Type: "copy", Folder: "Archive"}, model.SieveCondition{Field: "subject", Value: "notice"}, "copy"},
		{model.SieveAction{Type: "stop"}, model.SieveCondition{Field: "body", Value: "notice"}, "body"},
	}
	for _, item := range cases {
		t.Run(item.extension, func(t *testing.T) {
			rule := model.SieveRule{ID: "rule-" + item.extension, Name: "规则", Enabled: true, Conditions: []model.SieveCondition{item.condition}, Actions: []model.SieveAction{item.action}}
			extensions := []string{"fileinto", "imap4flags", "copy", "body", "vacation"}
			var available []string
			for _, extension := range extensions {
				if extension != item.extension {
					available = append(available, extension)
				}
			}
			_, err := Compile([]model.SieveRule{rule}, nil, available)
			var missing *ExtensionError
			if !errors.As(err, &missing) || missing.RuleID != rule.ID || missing.Extension != item.extension {
				t.Fatalf("扩展错误没有包含规则 ID 与扩展名称：%v", err)
			}
			if _, err := Compile([]model.SieveRule{rule}, nil, extensions); err != nil {
				t.Fatal(err)
			}
			rule.Enabled = false
			script, err := Compile([]model.SieveRule{rule}, nil, []string{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(script, "require [") {
				t.Fatal("停用规则仍要求服务器扩展")
			}
		})
	}
	_, err := Compile(nil, &model.Vacation{Enabled: true, Body: "离开期间自动回复", Days: 7}, []string{})
	var missing *ExtensionError
	if !errors.As(err, &missing) || missing.RuleID != "vacation" || missing.Extension != "vacation" {
		t.Fatalf("自动回复扩展校验失败：%v", err)
	}
}
