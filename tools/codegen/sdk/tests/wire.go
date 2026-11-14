package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"time"

	"github.com/Passion-Flow/ownsolid/sdk/go/datasets"
	"github.com/Passion-Flow/ownsolid/sdk/go/identity"
	"github.com/Passion-Flow/ownsolid/sdk/go/nodes"
	"github.com/google/uuid"
)

func main() {
	var input struct {
		Origin, Email, Password, DatasetID, DraftID, Payload string
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		log.Fatal("read wire fixture")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	httpClient := &http.Client{
		Transport: transport, Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer httpClient.CloseIdleConnections()
	ctx := context.Background()
	identities, err := identity.NewClientWithResponses(input.Origin, identity.WithHTTPClient(httpClient),
		identity.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Origin", input.Origin)
			return nil
		}))
	if err != nil {
		log.Fatal(err)
	}
	csrf, err := identities.GetCsrfWithResponse(ctx)
	if err != nil || csrf.JSON200 == nil {
		log.Fatal("read CSRF token")
	}
	login, err := identities.LoginWithResponse(ctx, &identity.LoginParams{XCSRFToken: csrf.JSON200.Data.Token},
		identity.LoginJSONRequestBody{Email: input.Email, Password: &input.Password})
	if err != nil || login.JSON200 == nil || login.JSON200.Data.Audience != "admin" {
		log.Fatal("log in through generated SDK")
	}
	nodeClient, err := nodes.NewClientWithResponses(input.Origin, nodes.WithHTTPClient(httpClient))
	if err != nil {
		log.Fatal(err)
	}
	limit := 3
	list, err := nodeClient.ListAdminNodesWithResponse(ctx, "default", "default", &nodes.ListAdminNodesParams{Limit: &limit})
	if err != nil || list.JSON200 == nil || len(list.JSON200.Data.Items) != 0 {
		log.Fatal("read scoped node list")
	}
	csrf, err = identities.GetCsrfWithResponse(ctx)
	if err != nil || csrf.JSON200 == nil {
		log.Fatal("read session CSRF token")
	}
	datasetClient, err := datasets.NewClientWithResponses(input.Origin, datasets.WithHTTPClient(httpClient))
	if err != nil {
		log.Fatal(err)
	}
	payload := []byte(input.Payload)
	problem, err := datasetClient.AdminUploadDatasetSourceWithBodyWithResponse(ctx, "default", "default",
		uuid.MustParse(input.DatasetID), uuid.MustParse(input.DraftID),
		&datasets.AdminUploadDatasetSourceParams{
			Origin: input.Origin, XCSRFToken: csrf.JSON200.Data.Token,
			XContentSHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), ContentLength: len(payload),
		}, "application/x-ndjson", bytes.NewReader(payload))
	if err != nil || problem.StatusCode() != http.StatusForbidden ||
		problem.ApplicationproblemJSONDefault == nil || problem.ApplicationproblemJSONDefault.Code != "ACCESS_DENIED" {
		log.Fatal("upload NDJSON and decode a problem response")
	}
}
