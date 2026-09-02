package middleware

import "testing"

func TestRevokeScopesDoNotCrossAdminBoundary(t *testing.T) {
	store := &Store{sess: map[string]*Session{
		"user":  {UserID: 1, IsAdmin: false},
		"admin": {UserID: 1, IsAdmin: true},
		"other": {UserID: 2, IsAdmin: false},
	}}
	store.RevokeUser(1)
	if _, ok := store.sess["user"]; ok {
		t.Fatal("普通用户会话未被撤销")
	}
	if _, ok := store.sess["admin"]; !ok {
		t.Fatal("撤销普通用户会话时误删管理员会话")
	}
	store.RevokeAdmin(1)
	if _, ok := store.sess["admin"]; ok {
		t.Fatal("管理员会话未被撤销")
	}
	if _, ok := store.sess["other"]; !ok {
		t.Fatal("撤销管理员会话时误删其他用户会话")
	}
}
