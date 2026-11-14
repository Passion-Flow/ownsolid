package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A collision reduces admission; it never grants ownership of another slot.
func mongoNativeSlotID(schema string, slot int) int64 {
	h := sha256.Sum256([]byte("projectone.mongodb.source." + schema + "." + strconv.Itoa(slot)))
	return int64(binary.BigEndian.Uint32(h[:4]) & 0x3fffffff)
}
func (s *Service) mongoLeaseSeconds() int {
	p := s.Identity.Config.MongoDB
	return p.TimeoutSeconds + 3*p.CloseSeconds + 2*s.policy().UploadSeconds + 4*s.policy().CommitSeconds
}
func (s *Service) withMongoSlot(ctx context.Context, operation func(context.Context) error) error {
	if ctx == nil || operation == nil {
		return failure("VALIDATION_FAILED")
	}
	repo, e := s.repository()
	if e != nil {
		return e
	}
	if repo.Dialect() != transaction.PostgreSQL && repo.Dialect() != transaction.Dameng {
		return failure("MONGODB_BUSINESS_DATABASE_UNSUPPORTED")
	}
	p := s.Identity.Config.MongoDB
	if p.Validate() != nil {
		return failure("MONGODB_CONFIGURATION_INVALID")
	}
	return withMongoSessionSlot(ctx, repo, p, s.mongoLeaseSeconds(), s.policy().CommitSeconds, operation)
}

// The borrowed session is used only for admission/release. Metadata operations
// use their own transactions while the same physical admission connection stays held.
func withMongoSessionSlot(ctx context.Context, repo transaction.Store, p config.MongoDBConfiguration, leaseSeconds, closeSeconds int, operation func(context.Context) error) error {
	if ctx == nil || repo == nil || operation == nil || p.Validate() != nil || leaseSeconds <= 0 || closeSeconds <= 0 {
		return failure("MONGODB_CONFIGURATION_INVALID")
	}
	if repo.Dialect() != transaction.PostgreSQL && repo.Dialect() != transaction.Dameng {
		return failure("MONGODB_BUSINESS_DATABASE_UNSUPPORTED")
	}
	owner, ok := repo.(transaction.SessionStore)
	if !ok {
		return failure("MONGODB_SESSION_STORE_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(leaseSeconds)*time.Second)
	defer cancel()
	e := owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) (result error) {
		schema := ""
		if session.Dialect() == transaction.Dameng {
			if e := session.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM DUAL`)).Scan(&schema); e != nil {
				return e
			}
			if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`).MatchString(schema) {
				return failure("MONGODB_SCHEMA_INVALID")
			}
		}
		for slot := 0; slot < p.MaximumConcurrent; slot++ {
			lock := mongoNativeSlotID(schema, slot)
			acquired := false
			if session.Dialect() == transaction.Dameng {
				var code int
				if e := session.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT DBMS_LOCK.REQUEST(?,6,0,FALSE) FROM DUAL`, lock)).Scan(&code); e != nil {
					return e
				}
				if code != 0 && code != 1 {
					return failure("MONGODB_SLOT_UNAVAILABLE")
				}
				acquired = code == 0
			} else {
				if e := session.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT pg_try_advisory_lock(hashtext('projectone.mongodb.source'),$1)", slot)).Scan(&acquired); e != nil {
					return e
				}
			}
			if !acquired {
				continue
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), time.Duration(closeSeconds)*time.Second)
				defer stop()
				if session.Dialect() == transaction.Dameng {
					var code int
					e := session.QueryRow(cleanup, transaction.SQL(transaction.Dameng, `SELECT DBMS_LOCK.RELEASE(?) FROM DUAL`, lock)).Scan(&code)
					if e != nil || code != 0 {
						result = errors.Join(result, failure("MONGODB_SLOT_RELEASE_UNCERTAIN"), e)
					}
				} else {
					var released bool
					e := session.QueryRow(cleanup, transaction.SQL(transaction.PostgreSQL, "SELECT pg_advisory_unlock(hashtext('projectone.mongodb.source'),$1)", slot)).Scan(&released)
					if e != nil || !released {
						result = errors.Join(result, failure("MONGODB_SLOT_RELEASE_UNCERTAIN"), e)
					}
				}
			}()
			return operation(ctx)
		}
		return failure("MONGODB_BUSY")
	})
	return mongoOutcome(e)
}
