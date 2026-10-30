package httpapi

import (
	"context"
	"testing"
	"time"
)

// 下游处理器不能继承"查账号"那一步的 5 秒预算。
//
// 这条不变量是实测出来的：工具循环让一次回答合法地超过 5 秒，
// 而中间件曾把查询用的超时上下文一并传下去，长回答就被误判成依赖超时。
func TestIdentityContextCarriesNoDeadline(t *testing.T) {
	parent := context.Background()
	got := identityContext(parent, userIdentity{ID: 7, Username: "teacher_a", Role: "teacher", ClassID: 1})
	if deadline, ok := got.Deadline(); ok {
		t.Fatalf("下游上下文不应带截止时间，实际 = %s", deadline.Format(time.RFC3339))
	}
	user, ok := got.Value(identityKey{}).(userIdentity)
	if !ok || user.ID != 7 || user.Role != "teacher" {
		t.Fatalf("账号没有正确放进上下文：%+v", got.Value(identityKey{}))
	}
}

// 父上下文自己有截止时间时会继承：这里继承的是请求本身的生命周期，不是查询预算。
func TestIdentityContextKeepsParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, ok := identityContext(parent, userIdentity{}).Deadline(); !ok {
		t.Fatal("父上下文的截止时间应当被继承")
	}
}
