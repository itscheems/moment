package cli

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/spf13/pflag"
)

func registerEmailRegistration(application *Application) {
	application.MustRegisterLeaf(LeafSpec{Path: []string{"auth", "request-email-code"}, Short: "Request an email verification code", Long: "Ask the running instance to deliver a registration code. Email delivery uses the server configuration.", Inputs: "Required --email; API mode only.", Output: "Challenge identifier, expiry, and earliest resend time.", Errors: "Delivery availability, registration policy and resend limits are enforced.", Example: "moment auth request-email-code --url http://127.0.0.1:18080 --email reader@example.com --json", Capability: CapabilityAPIOnly, Flags: func(flags *pflag.FlagSet) { flags.String("email", "", "email address to verify") }, Run: func(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
		email, _ := input.String("email")
		if strings.TrimSpace(email) == "" {
			return Result{}, invalidArgument("--email is required", "supply the address to verify")
		}
		var receipt app.EmailChallengeReceipt
		meta, err := anonymousEmailRequest(ctx, inv, "/api/v1/register/email", map[string]string{"email": email}, &receipt)
		if err != nil {
			return Result{}, err
		}
		return Result{Data: receipt, Meta: meta, Text: "Verification code requested"}, nil
	}})
	application.MustRegisterLeaf(LeafSpec{Path: []string{"auth", "verify-email-code"}, Short: "Verify a registration email code", Long: "Exchange the code received by email for a one-time registration proof. Keep the proof private and pass it to auth register using a private file.", Inputs: "Required --challenge and --code-file. API mode only.", Output: "Private verification token, verified address, and expiry.", Errors: "Expired codes and exhausted verification attempts are refused.", Example: "moment auth verify-email-code --url http://127.0.0.1:18080 --challenge <challenge-id> --code-file ./code.txt --json", Capability: CapabilityAPIOnly, Flags: func(flags *pflag.FlagSet) {
		flags.String("challenge", "", "identifier returned by request-email-code")
		flags.String("code-file", "", "private file containing the code, or - for stdin")
	}, Run: func(ctx context.Context, inv Invocation, input CommandInput) (Result, error) {
		challenge, _ := input.String("challenge")
		file, _ := input.String("code-file")
		if challenge == "" || file == "" {
			return Result{}, invalidArgument("--challenge and --code-file are required", "use the challenge and code from the verification request")
		}
		code, err := readPasswordInput(input.In(), file)
		if err != nil {
			return Result{}, err
		}
		var receipt app.VerifiedEmailReceipt
		meta, err := anonymousEmailRequest(ctx, inv, "/api/v1/register/email/verify", map[string]string{"challenge_id": challenge, "code": code}, &receipt)
		if err != nil {
			return Result{}, err
		}
		return Result{Data: receipt, Meta: meta, Text: "Email verified; use --json to retrieve the private registration proof"}, nil
	}})
}

func anonymousEmailRequest(ctx context.Context, inv Invocation, path string, payload any, receipt any) (map[string]any, error) {
	target, err := inv.APITransport()
	if err != nil {
		return nil, err
	}
	request, err := newAPIRequest(ctx, target, http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, NewCommandError("EMAIL_UNAVAILABLE", "email request could not be confirmed", "retry after checking the connection", ExitUnavailable).WithCause(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) {
		return nil, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusOK {
		return nil, decodeAPIFailure(response.StatusCode, body)
	}
	_, meta, err := decodeStrictAPISuccess(body, receipt)
	return meta, err
}
