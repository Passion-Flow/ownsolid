package middleware

import (
 "strconv"
 "github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The module Policy accepts pages 1..200; list queries request one extra row.
// This is a structural SQL boundary, not a second adjustable policy owner.
const maximumNativePageSize = 200

func nativeLimit(n int) string {
 if n < 1 || n > maximumNativePageSize+1 { return "" }
 return strconv.Itoa(n)
}
func pageStatement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
 if q.Dialect() != transaction.Dameng { return statement(q, pg, my, args...) }
 if len(args) == 0 { return transaction.SQL(transaction.Dameng, "") }
 n, ok := args[len(args)-1].(int)
 if !ok || nativeLimit(n) == "" { return transaction.SQL(transaction.Dameng, "") }
 return transaction.SQL(transaction.Dameng, dm, args[:len(args)-1]...)
}
