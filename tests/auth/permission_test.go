package auth_test

import (
	"testing"

	"github.com/exzco/bbs/internal/auth"
	"github.com/exzco/bbs/internal/models"
)

func TestInheritedPermissionsAndSeparateReply(t *testing.T) {
	parent := models.Tag{ID: 10, IsRestricted: true}
	child := models.Tag{ID: 11, ParentID: &parent.ID}
	tp := auth.TagPerms{Tags: map[uint]models.Tag{10: parent}, View: map[uint][]uint{10: {models.GroupMember}}, Post: map[uint][]uint{10: {models.GroupMod}}, Reply: map[uint][]uint{10: {models.GroupMember}}}
	auth.FillTagPerm(nil, &child, tp)
	member := &models.User{ID: 1}
	if !auth.CanViewTag(member, &child) || !auth.CanReplyTag(member, &child) || auth.CanPostTag(member, &child) {
		t.Fatal("parent permissions or separate reply semantics lost")
	}
	if auth.CanViewTag(nil, &child) {
		t.Fatal("guest bypassed restricted parent")
	}
	tp.Failed = true
	auth.FillTagPerm(nil, &child, tp)
	if auth.CanViewTag(member, &child) || auth.CanReplyTag(member, &child) {
		t.Fatal("failed permission load must deny access")
	}
}
