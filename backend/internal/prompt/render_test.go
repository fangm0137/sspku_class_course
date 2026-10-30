package prompt

import (
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC) }

// 三个占位符都被替换成实际值。
func TestRenderSubstitutesAllPlaceholders(t *testing.T) {
	body := "你是{{app_name}}的助教。今天是{{today}}。学生问：{{question}}"
	got := RenderAt(body, "数学助教", "一元二次方程怎么解", fixedNow())
	want := "你是数学助教的助教。今天是2026-10-30。学生问：一元二次方程怎么解"
	if got != want {
		t.Fatalf("渲染结果不符\n got=%q\nwant=%q", got, want)
	}
	if strings.Contains(got, "{{") || strings.Contains(got, "}}") {
		t.Fatalf("渲染结果仍含占位符语法：%q", got)
	}
}

// 未识别的占位符原样保留，不静默清空。
func TestRenderKeepsUnknownPlaceholders(t *testing.T) {
	got := RenderAt("班级是{{class_name}}，今天{{today}}", "语文助教", "问题", fixedNow())
	if !strings.Contains(got, "{{class_name}}") {
		t.Fatalf("未知占位符被改写：%q", got)
	}
	if !strings.Contains(got, "2026-10-30") {
		t.Fatalf("已知占位符未被替换：%q", got)
	}
}

// 替换只做一次：提问里的占位符字面量不得被二次替换。
func TestRenderIsNotRecursive(t *testing.T) {
	got := RenderAt("学生问：{{question}}", "通用答疑", "请解释 {{today}} 是什么", fixedNow())
	want := "学生问：请解释 {{today}} 是什么"
	if got != want {
		t.Fatalf("替换是递归的\n got=%q\nwant=%q", got, want)
	}
	if strings.Contains(got, "2026-10-30") {
		t.Fatalf("提问内容被当作模板二次渲染：%q", got)
	}
}

// 应用名里的占位符字面量同样不应被二次替换。
func TestRenderDoesNotRescanAppName(t *testing.T) {
	got := RenderAt("{{app_name}}", "{{today}}助教", "问题", fixedNow())
	if got != "{{today}}助教" {
		t.Fatalf("应用名被二次渲染：%q", got)
	}
}

// 模板体的必填与长度约束：空、超长、含 NUL 都被拒。
func TestValidateRejectsMalformedBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
		desc string
	}{
		{"空模板体", "", ""},
		{"仅空白", "   \n\t ", ""},
		{"超长模板体", strings.Repeat("字", MaxBodyRunes+1), ""},
		{"含 NUL", "正常文本\x00尾巴", ""},
		{"说明超长", "正文", strings.Repeat("说", MaxDescRunes+1)},
	}
	for _, item := range cases {
		if err := validate("模板名", item.body, item.desc); err == nil {
			t.Fatalf("%s 应被拒绝", item.name)
		}
	}
	if err := validate("模板名", strings.Repeat("字", MaxBodyRunes), ""); err != nil {
		t.Fatalf("边界长度应通过：%v", err)
	}
	if err := validate(strings.Repeat("名", MaxNameRunes+1), "正文", ""); err == nil {
		t.Fatal("超长名称应被拒绝")
	}
}
