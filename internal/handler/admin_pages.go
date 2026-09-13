package handler

import (
	"net/http"
	"strconv"
)

func (a *Admin) require(w http.ResponseWriter, r *http.Request) bool {
	return adminRequire(w, r)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
