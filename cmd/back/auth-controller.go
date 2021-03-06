package main

import (
	"context"
	"encoding/json"
	"github.com/WantsToFress/hackathon-backend/internal/model"
	resequip "github.com/WantsToFress/hackathon-backend/pkg"
	"net/http"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/go-pg/pg/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type tokenContextKey struct{}

func userToContext(ctx context.Context, user *resequip.Person) context.Context {
	return context.WithValue(ctx, tokenContextKey{}, user)
}

func userFromContext(ctx context.Context) (*resequip.Person, error) {
	user, ok := ctx.Value(tokenContextKey{}).(*resequip.Person)
	if !ok {
		return nil, status.Error(codes.Internal, "user not present in context")
	}
	return user, nil
}

type UserClaims struct {
	Login      string    `json:"login"`
	Expiration time.Time `json:"exp"`
}

func (u *UserClaims) Valid() error {
	if u.Login == "" {
		return status.Error(codes.InvalidArgument, "empty login")
	}
	return nil
}

func (is *IncidentService) AuthInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	meta, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata not present in context")
	}

	authMeta := meta.Get("authorization")
	if len(authMeta) == 0 {
		return nil, status.Error(codes.Unauthenticated, "authorization header not present")
	}

	authRaw := authMeta[0]

	token, err := jwt.ParseWithClaims(authRaw, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		return is.publicKey, nil
	})
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	userClaims, ok := token.Claims.(*UserClaims)
	if !ok {
		return nil, status.Error(codes.Internal, "invalid claims type")
	}

	user, err := is.getPersonByLogin(ctx, userClaims.Login)
	if err != nil {
		return nil, err
	}

	return handler(userToContext(ctx, user), req)
}

func (is *IncidentService) newToken(login string) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodRS256, &UserClaims{
		Login:      login,
		Expiration: time.Now().Add(time.Hour * 24 * 3),
	}).SignedString(is.privateKey)
}

func (is *IncidentService) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type registerRequest struct {
		Login    string `json:"login"`
		Email    string `json:"email"`
		Password string `json:"password"`
		FullName string `json:"full_name"`
	}
	req := &registerRequest{}

	err := json.NewDecoder(r.Body).Decode(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user := &model.Person{
		ID:       model.GenStringUUID(),
		FullName: req.FullName,
		Login:    req.Login,
		Password: req.Password,
		Email:    req.Email,
		Role:     resequip.Role_employee.String(),
	}

	_, err = is.db.ModelContext(ctx, user).
		Insert()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	token, err := is.newToken(req.Login)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Authorization", token)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}

func (is *IncidentService) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type loginRequest struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	req := &loginRequest{}

	err := json.NewDecoder(r.Body).Decode(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = is.db.ModelContext(ctx, &model.Person{}).
		Where(model.Columns.Person.Login+" = ?", req.Login).
		Where(model.Columns.Person.Password+" = ?", req.Password).
		First()
	if err != nil {
		if err == pg.ErrNoRows {
			http.Error(w, "user with given credentials doesn't exist", http.StatusUnauthorized)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	token, err := is.newToken(req.Login)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Authorization", token)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}
