package applications

import (
	"crypto/hmac"
	"encoding/base64"
	"net/url"
	"strings"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const maximumConversationSearch = 120

type conversationSearch struct {
	term, scope, session string
	key                  []byte
}

// prepareConversationSearch consumes q and unwraps a search cursor in the
// service's private copy of the query. An unfiltered query keeps its old format.
func prepareConversationSearch(options url.Values, scope, session string, key []byte) (conversationSearch, error) {
	values, found := options[pagination.ListSearchParameter]
	if !found {
		return conversationSearch{}, nil
	}
	if len(values) != 1 || !pagination.SearchTerm(values[0], maximumConversationSearch) || strings.TrimSpace(values[0]) == "" {
		return conversationSearch{}, failure("VALIDATION_FAILED")
	}
	if !strict.ValidID(session) {
		return conversationSearch{}, failure("AUTH_SESSION_EXPIRED")
	}
	if len(key) < security.MinimumHMACKeyBytes || len(key) > security.MaximumHMACKeyBytes {
		return conversationSearch{}, failure("SERVICE_NOT_READY")
	}
	search := conversationSearch{term: values[0], scope: scope, session: session, key: key}
	if cursors, found := options["cursor"]; found {
		if len(cursors) != 1 {
			return conversationSearch{}, failure("VALIDATION_FAILED")
		}
		raw := cursors[0]
		if len(raw) > pagination.MaximumCursorBytes {
			return conversationSearch{}, failure("INVALID_CURSOR")
		}
		position, tag, ok := strings.Cut(raw, ".")
		if !ok || position == "" || !hmac.Equal([]byte(tag), []byte(search.tag(position))) {
			return conversationSearch{}, failure("INVALID_CURSOR")
		}
		options.Set("cursor", position)
	}
	delete(options, pagination.ListSearchParameter)
	return search, nil
}

func (s conversationSearch) tag(position string) string {
	return base64.RawURLEncoding.EncodeToString(security.MAC(s.key,
		"application-conversation-search/v1", s.scope, s.session, s.term, position))
}
