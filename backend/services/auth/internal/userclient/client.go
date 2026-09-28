package userclient

import (
	"context"

	"rokko.dev/gen/user/v1"
	"rokko.dev/pkg/grpcx"
	"rokko.dev/pkg/id"
)

type Client struct{ api userv1.UserServiceClient }

func New(api userv1.UserServiceClient) *Client { return &Client{api: api} }

func (c *Client) Ensure(ctx context.Context, accessToken string, userID int64, username string) error {
	_, err := c.api.EnsureProfile(grpcx.WithToken(ctx, accessToken), &userv1.EnsureProfileRequest{
		UserId:   id.Format(userID),
		Username: username,
	})
	return err
}
