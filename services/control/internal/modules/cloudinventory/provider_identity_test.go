package cloudinventory

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestAWSCallerPartitionAndPrincipal(t *testing.T) {
	for _, value := range []string{"", "arn:aws-cn:iam::123456789012:user/a", "arn:aws:iam::999999999999:user/a", "arn:aws:ec2::123456789012:instance/a", "arn:aws:sts:us-east-1:123456789012:assumed-role/a/b", "arn:aws:iam::123456789012:", "arn:aws:iam::123456789012:user/a\n"} {
		t.Run(fmt.Sprintf("invalid_%x", value), func(t *testing.T) {
			var calls atomic.Int32
			p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprintf(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123456789012</Account><Arn>%s</Arn></GetCallerIdentityResult></GetCallerIdentityResponse>`, value)
			})
			out, e := p.Discover(context.Background(), a, "us-east-1")
			wantCode(t, e, "AWS_ACCOUNT_MISMATCH")
			if out != nil || calls.Load() != 1 {
				t.Fatal("identity mismatch proceeded to EC2")
			}
		})
	}
	for _, partition := range []string{"aws", "aws-cn", "aws-us-gov"} {
		a := config.AWSConnection{AccountID: "123456789012", Partition: partition}
		for _, resource := range []string{"iam::123456789012:user/a", "iam::123456789012:root", "sts::123456789012:assumed-role/a/b", "sts::123456789012:federated-user/a"} {
			if !callerARN("arn:"+partition+":"+resource, a) {
				t.Fatal("valid principal refused", partition, resource)
			}
		}
	}
}

func TestAWSDeadlineWhileReadingResponse(t *testing.T) {
	var calls atomic.Int32
	p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	p.Policy.RetryAttempts = 1
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	out, e := p.Discover(ctx, a, "us-east-1")
	wantCode(t, e, "AWS_CANCELLED")
	if out != nil || calls.Load() != 1 {
		t.Fatal("cancelled body read retried or returned data")
	}
}

func TestAWSItemBoundAndBodyRedaction(t *testing.T) {
	p, a := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("Action") == "GetCallerIdentity" {
			caller(w)
			return
		}
		one := page("i-0123456789abcdef0", "")
		two := page("i-0123456789abcdef1", "")
		extra := strings.Split(strings.Split(two, "<reservationSet>")[1], "</reservationSet>")[0]
		fmt.Fprint(w, strings.Replace(one, "</reservationSet>", extra+"</reservationSet>", 1))
	})
	p.Policy.MaximumResources = 1
	out, e := p.Discover(context.Background(), a, "us-east-1")
	wantCode(t, e, "AWS_LIMIT_REACHED")
	if out != nil {
		t.Fatal("partial bounded snapshot escaped")
	}
	p, a = providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `<ErrorResponse><Error><Code>AccessDenied</Code><Message>SECRET_RESPONSE_SENTINEL</Message></Error></ErrorResponse>`)
	})
	_, e = p.Discover(context.Background(), a, "us-east-1")
	wantCode(t, e, "AWS_ACCESS_DENIED")
}
