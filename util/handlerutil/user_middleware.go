package handlerutil

import (
	"log"
	"net/http"

	"github.com/dgrijalva/jwt-go"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"sourcegraph.com/sqs/pbtypes"
	appauth "src.sourcegraph.com/sourcegraph/app/auth"
	"src.sourcegraph.com/sourcegraph/auth"
	"src.sourcegraph.com/sourcegraph/go-sourcegraph/sourcegraph"
	"src.sourcegraph.com/sourcegraph/util/httputil/httpctx"
)

// This file contains getters and middleware setters for data that
// should be in the context during HTTP handler execution.

type contextKey int

const (
	userKey contextKey = iota
	fullUserKey
	emailAddrKey
)

// UserMiddleware fetches the user object and stores it in the context
// for downstream HTTP handlers. The CookieMiddleware must already
// have run (or something else that calls sourcegraph.WithCredentials
// based on the request's auth).
func UserMiddleware(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := httpctx.FromRequest(r)

	cred := sourcegraph.CredentialsFromContext(ctx)
	if cred != nil && UserFromRequest(r) == nil && fetchUserForCredentials(cred) {
		if authInfo, user, email := identifyUser(ctx, w); authInfo != nil {
			// This code should be kept in sync with ClearUser and WithUser.
			ctx = withUser(ctx, authInfo.UserSpec())
			ctx = withFullUser(ctx, user)
			ctx = withEmail(ctx, email)
			ctx = auth.WithActor(ctx, auth.Actor{
				UID:      int(authInfo.UID),
				Login:    authInfo.Login,
				ClientID: authInfo.ClientID,
				Scope:    auth.UnmarshalScope(authInfo.Scopes),
			})
		}
	}

	httpctx.SetForRequest(r, ctx)
	next(w, r)
}

// ClearUser removes user, full user, actor and and credentials from context.
// It should unset all context values that UserMiddleware has set.
func ClearUser(ctx context.Context) context.Context {
	ctx = withUser(ctx, nil)
	ctx = withFullUser(ctx, nil)
	ctx = withEmail(ctx, "")
	ctx = auth.WithActor(ctx, auth.Actor{})
	ctx = sourcegraph.WithCredentials(ctx, nil)
	return ctx
}

// WithUser returns a copy of the context with the user and full user added to it
// (available via UserFromContext and FullUserFromContext).
//
// To clear the user, ClearUser should be used instead.
//
// Generally you should use UserMiddleware to set it in the context;
// WithUser should only be used for tests where you want to inject
// a specific user.
func WithUser(ctx context.Context, user sourcegraph.UserSpec) context.Context {
	ctx = withUser(ctx, &user)
	ctx = withFullUser(ctx, &sourcegraph.User{
		Login: user.Login,
		UID:   user.UID,
	})
	return ctx
}

func identifyUser(ctx context.Context, w http.ResponseWriter) (*sourcegraph.AuthInfo, *sourcegraph.User, string) {
	cl, err := sourcegraph.NewClientFromContext(ctx)
	if err != nil {
		log.Printf("warning: identifying current user failed: %s (continuing, deleting cookie)", err)
		appauth.DeleteSessionCookie(w)
		return nil, nil, ""
	}

	// Call to Identify will be authenticated with the
	// session's access token (because of previous middleware).
	authInfo, err := cl.Auth.Identify(ctx, &pbtypes.Void{})
	if err != nil {
		log.Printf("warning: identifying current user failed: %s (continuing, deleting cookie)", err)
		appauth.DeleteSessionCookie(w)
		return nil, nil, ""
	}

	if authInfo.UID == 0 {
		// The cookie was probably created by another server; delete it.
		log.Printf("warning: credentials don't identify a user on this server (continuing, deleting cookie)")
		appauth.DeleteSessionCookie(w)
		return nil, nil, ""
	}

	// Fetch full user.
	user, err := cl.Users.Get(ctx, authInfo.UserSpec())
	if err != nil {
		if grpc.Code(err) != codes.Unimplemented && grpc.Code(err) != codes.Unauthenticated {
			log.Printf("warning: fetching full user failed: %s (continuing, deleting cookie)", err)
			appauth.DeleteSessionCookie(w)
		}
		return nil, nil, ""
	}

	// Fetch user emails.
	userSpec := user.Spec()
	email := "" // primary email address
	emails, err := cl.Users.ListEmails(ctx, &userSpec)
	if err != nil {
		if grpc.Code(err) == codes.PermissionDenied || user.IsOrganization {
			// We are not allowed to view the emails or its an org and orgs don't have emails
			// so just show an empty email.
			return authInfo, user, email
		} else {
			log.Printf("warning: fetching user emails failed: %s (continuing, deleting cookie)", err)
			return nil, nil, ""
		}
	}

	for _, elem := range emails.EmailAddrs {
		if elem.Primary {
			return authInfo, user, elem.Email
		}
	}

	return authInfo, user, email
}

// fetchUserForCredentials is whether UserMiddleware should try to
// fetch the user object, given the specified credentials. It returns
// true if cred represents a user. If it just represents an authed
// client (or nothing), it returns false.
func fetchUserForCredentials(cred sourcegraph.Credentials) bool {
	tok0, err := cred.Token()
	if err != nil {
		// Return true so it tries to use these creds and deletes them
		// from the session if they are invalid.
		return true
	}
	tok, _ := jwt.Parse(tok0.AccessToken, func(*jwt.Token) (interface{}, error) { return nil, nil })
	if tok == nil {
		return false
	}
	_, hasUID := tok.Claims["UID"]
	return hasUID
}

// UserFromRequest returns the request's context's authenticated user (if any).
func UserFromRequest(r *http.Request) *sourcegraph.UserSpec {
	return UserFromContext(httpctx.FromRequest(r))
}

// UserFromContext returns the context's authenticated user (if any).
func UserFromContext(ctx context.Context) *sourcegraph.UserSpec {
	user, _ := ctx.Value(userKey).(*sourcegraph.UserSpec)
	return user
}

// withUser returns a copy of the context with the user added to it
// (and available via UserFromContext).
func withUser(ctx context.Context, user *sourcegraph.UserSpec) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// FullUserFromRequest returns the request's context's authenticated full user (if any).
func FullUserFromRequest(r *http.Request) *sourcegraph.User {
	return FullUserFromContext(httpctx.FromRequest(r))
}

// FullUserFromContext returns the context's authenticated full user (if any).
func FullUserFromContext(ctx context.Context) *sourcegraph.User {
	user, _ := ctx.Value(fullUserKey).(*sourcegraph.User)
	return user
}

// EmailFromRequest returns the request's context's primary email for the user (if any).
func EmailFromRequest(r *http.Request) string {
	return EmailFromContext(httpctx.FromRequest(r))
}

// EmailFromContext returns the context's primary email list for the user user (if any).
func EmailFromContext(ctx context.Context) string {
	email, _ := ctx.Value(emailAddrKey).(string)
	return email
}

// withFullUser returns a copy of the context with the full user added to it
// (and available via FullUserFromContext).
func withFullUser(ctx context.Context, user *sourcegraph.User) context.Context {
	return context.WithValue(ctx, fullUserKey, user)
}

// withEmail returns a copy of the context with the primary email added to it
// (and available via UserEmailFromContext).
func withEmail(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, emailAddrKey, email)
}
