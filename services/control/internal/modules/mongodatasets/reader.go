// Package mongodatasets reads a single granted collection without source writes.
package mongodatasets

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type Mapping struct {
	Prompt     string `json:"prompt_field"`
	Completion string `json:"completion_field"`
	Conversion string `json:"conversion"`
}
type Result struct {
	Data        []byte `json:"-"`
	Rows        int64  `json:"rows"`
	FirstID     string `json:"first_id"`
	LastID      string `json:"last_id"`
	Consistency string `json:"consistency"`
}

func (m Mapping) Validate(s config.MongoDBSource) error {
	allowed := map[string]bool{}
	for _, f := range s.Fields {
		allowed[f] = true
	}
	if !allowed[m.Prompt] || !allowed[m.Completion] || strings.HasPrefix(m.Prompt, m.Completion+".") || strings.HasPrefix(m.Completion, m.Prompt+".") || m.Conversion != "string" && m.Conversion != "extended_json" {
		return errors.New("MONGODB_MAPPING_INVALID")
	}
	return nil
}
func sourceOptions(s config.MongoDBSource, p config.MongoDBConfiguration) (*options.ClientOptions, error) {
	bad := errors.New("MONGODB_CONFIGURATION_INVALID")
	raw, e := config.MongoDBSecret(s.URIFile)
	if e != nil {
		return nil, bad
	}
	var ca []byte
	if s.TLS && s.CAFile != "" {
		ca, e = config.MongoDBCA(s.CAFile)
		if e != nil {
			return nil, bad
		}
	}
	return sourceOptionsBytes(s, p, raw, ca)
}
func sourceOptionsBytes(s config.MongoDBSource, p config.MongoDBConfiguration, raw, ca []byte) (*options.ClientOptions, error) {
	bad := errors.New("MONGODB_CONFIGURATION_INVALID")
	uri := strings.TrimSpace(string(raw))
	u, e := url.Parse(uri)
	if e != nil || u.Scheme != "mongodb" && u.Scheme != "mongodb+srv" || u.Host == "" || u.Fragment != "" || u.User == nil || u.User.Username() == "" {
		return nil, bad
	}
	if password, ok := u.User.Password(); !ok || password == "" {
		return nil, bad
	}
	// The operator URI selects endpoints/authentication only. TLS, retries, pool and time budgets have one typed owner.
	allowed := map[string]bool{"authSource": true, "authMechanism": true, "replicaSet": true, "directConnection": true}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return nil, bad
	}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 || values[0] == "" {
			return nil, bad
		}
		if key == "directConnection" && values[0] != "true" && values[0] != "false" {
			return nil, bad
		}
	}
	if u.Path != "" && u.Path != "/" && u.Path != "/"+s.Database {
		return nil, bad
	}
	if u.Scheme == "mongodb+srv" && !s.TLS {
		return nil, bad
	}
	o := options.Client().ApplyURI(uri).SetRetryReads(false).SetRetryWrites(false).SetMaxPoolSize(1).SetMinPoolSize(0).SetMaxConnecting(1).SetReadPreference(readpref.Primary()).SetConnectTimeout(time.Duration(p.TimeoutSeconds) * time.Second).SetServerSelectionTimeout(time.Duration(p.TimeoutSeconds) * time.Second).SetTimeout(time.Duration(p.TimeoutSeconds) * time.Second)
	if s.TLS {
		t := &tls.Config{MinVersion: tls.VersionTLS12}
		if s.CAFile != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(ca) {
				return nil, bad
			}
			t.RootCAs = pool
		}
		o.SetTLSConfig(t)
	} else {
		o.SetTLSConfig(nil)
	}
	if o.Validate() != nil {
		return nil, bad
	}
	return o, nil
}

// Read never resumes a cursor from an earlier session. An error returns no partial data.
func Read(ctx context.Context, s config.MongoDBSource, p config.MongoDBConfiguration, m Mapping, limits wire.Limits, preflight bool) (Result, error) {
	if e := m.Validate(s); e != nil {
		return Result{}, e
	}
	o, e := sourceOptions(s, p)
	if e != nil {
		return Result{}, e
	}
	return readOptions(ctx, s, p, m, limits, preflight, o)
}

func readOptions(ctx context.Context, s config.MongoDBSource, p config.MongoDBConfiguration, m Mapping, limits wire.Limits, preflight bool, o *options.ClientOptions) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	client, e := mongo.Connect(o)
	if e != nil {
		return Result{}, errors.New("MONGODB_CONNECT_FAILED")
	}
	cleanup := func(fn func(context.Context)) {
		c, stop := context.WithTimeout(context.Background(), time.Duration(p.CloseSeconds)*time.Second)
		defer stop()
		fn(c)
	}
	defer cleanup(func(c context.Context) { _ = client.Disconnect(c) })
	session, e := client.StartSession(options.Session().SetSnapshot(true).SetCausalConsistency(false))
	if e != nil {
		return Result{}, safeError(e)
	}
	defer cleanup(session.EndSession)
	ctx = mongo.NewSessionContext(ctx, session)
	projection := bson.D{{Key: "_id", Value: 1}, {Key: m.Prompt, Value: 1}}
	if m.Prompt != m.Completion {
		projection = append(projection, bson.E{Key: m.Completion, Value: 1})
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetProjection(projection).SetBatchSize(p.BatchSize).SetLimit(limits.MaximumRows + 1)
	if preflight {
		opts.SetLimit(1)
	}
	cursor, e := client.Database(s.Database).Collection(s.Collection).Find(ctx, bson.D{}, opts)
	if e != nil {
		return Result{}, safeError(e)
	}
	defer cleanup(func(c context.Context) { _ = cursor.Close(c) })
	result := Result{Consistency: "snapshot-session"}
	var out bytes.Buffer
	for cursor.Next(ctx) {
		if e = appendDocument(&out, &result, cursor.Current, m, limits); e != nil {
			return Result{}, e
		}
	}
	if e = cursor.Err(); e != nil {
		return Result{}, safeError(e)
	}
	if ctx.Err() != nil {
		return Result{}, errors.New("MONGODB_TIMEOUT")
	}
	if !preflight && result.Rows == 0 {
		return Result{}, errors.New("MONGODB_SOURCE_EMPTY")
	}
	result.Data = out.Bytes()
	return result, nil
}
func safeError(e error) error {
	if errors.Is(e, context.DeadlineExceeded) || mongo.IsTimeout(e) {
		return errors.New("MONGODB_TIMEOUT")
	}
	if errors.Is(e, context.Canceled) {
		return errors.New("MONGODB_CANCELLED")
	}
	var ce mongo.CommandError
	if errors.As(e, &ce) {
		switch ce.Code {
		case 13, 18:
			return errors.New("MONGODB_ACCESS_DENIED")
		case 246, 286:
			return errors.New("MONGODB_SNAPSHOT_EXPIRED")
		case 72, 303:
			return errors.New("MONGODB_SNAPSHOT_UNSUPPORTED")
		}
	}
	return errors.New("MONGODB_READ_FAILED")
}
func value(raw bson.Raw, path, mode string) (string, error) {
	v, e := raw.LookupErr(strings.Split(path, ".")...)
	if e != nil || v.Type == bson.TypeNull || v.Type == bson.TypeUndefined {
		return "", errors.New("MONGODB_FIELD_INVALID")
	}
	if text, ok := v.StringValueOK(); ok {
		return text, nil
	}
	if mode == "string" {
		return "", errors.New("MONGODB_TYPE_INVALID")
	}
	// Marshal one BSON value through canonical Extended JSON so int64, decimal, dates and binary retain their type/precision.
	var decoded any
	if v.Unmarshal(&decoded) != nil {
		return "", errors.New("MONGODB_TYPE_INVALID")
	}
	rawJSON, e := bson.MarshalExtJSON(bson.D{{Key: "value", Value: decoded}}, true, false)
	if e != nil {
		return "", errors.New("MONGODB_TYPE_INVALID")
	}
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(rawJSON, &wrapper) != nil {
		return "", errors.New("MONGODB_TYPE_INVALID")
	}
	return string(wrapper["value"]), nil
}
func Convert(raw bson.Raw, m Mapping, limits wire.Limits) ([]byte, error) {
	id, ok := raw.Lookup("_id").ObjectIDOK()
	if !ok {
		return nil, errors.New("MONGODB_IDENTIFIER_INVALID")
	}
	prompt, e := value(raw, m.Prompt, m.Conversion)
	if e != nil {
		return nil, e
	}
	completion, e := value(raw, m.Completion, m.Conversion)
	if e != nil {
		return nil, e
	}
	line, e := (wire.Record{SourceID: id.Hex(), Prompt: prompt, Completion: completion}).Canonical(limits)
	if e != nil {
		return nil, errors.New("MONGODB_RECORD_INVALID")
	}
	return line, nil
}

func appendDocument(out *bytes.Buffer, result *Result, raw bson.Raw, m Mapping, limits wire.Limits) error {
	id, ok := raw.Lookup("_id").ObjectIDOK()
	if !ok {
		return errors.New("MONGODB_IDENTIFIER_INVALID")
	}
	current := id.Hex()
	if result.LastID != "" && current <= result.LastID {
		return errors.New("MONGODB_ORDER_INVALID")
	}
	record, e := Convert(raw, m, limits)
	if e != nil {
		return e
	}
	if result.Rows+1 > limits.MaximumRows || int64(out.Len()+len(record)+1) > limits.MaximumBytes {
		return errors.New("MONGODB_LIMIT_EXCEEDED")
	}
	if result.Rows == 0 {
		result.FirstID = current
	}
	result.LastID = current
	result.Rows++
	out.Write(record)
	out.WriteByte('\n')
	return nil
}
