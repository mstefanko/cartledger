package api

import "errors"

const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

type sqliteCodeError interface {
	Code() int
}

func isUniqueConstraintError(err error) bool {
	var codeErr sqliteCodeError
	if !errors.As(err, &codeErr) {
		return false
	}
	switch codeErr.Code() {
	case sqliteConstraintPrimaryKey, sqliteConstraintUnique:
		return true
	default:
		return false
	}
}
