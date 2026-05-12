package sqliteutil

import "errors"

const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

type sqliteErrorCode interface {
	Code() int
}

func IsUniqueConstraint(err error) bool {
	var sqliteErr sqliteErrorCode
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() {
	case sqliteConstraintPrimaryKey, sqliteConstraintUnique:
		return true
	default:
		return false
	}
}
