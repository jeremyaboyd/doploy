// Package doclient wraps the DigitalOcean API client with pagination helpers.
package doclient

import (
	"context"
	"fmt"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/config"
)

// UserAgent identifies doploy to the DigitalOcean API.
const UserAgent = "doploy"

// New builds an authenticated API client from stored or environment credentials.
func New(version string) (*godo.Client, error) {
	creds, err := config.Load()
	if err != nil {
		return nil, err
	}
	return NewWithToken(creds.Token, version)
}

// NewWithToken builds a client for an explicit token, used by `auth init` to
// validate a token before saving it.
func NewWithToken(token, version string) (*godo.Client, error) {
	client := godo.NewFromToken(token)
	client.UserAgent = fmt.Sprintf("%s/%s", UserAgent, version)
	return client, nil
}

// Paginate walks every page of a list endpoint and returns the accumulated
// results. The callback receives the list options to pass through to godo.
func Paginate[T any](fn func(opt *godo.ListOptions) ([]T, *godo.Response, error)) ([]T, error) {
	var all []T
	opt := &godo.ListOptions{PerPage: 200}

	for {
		page, resp, err := fn(opt)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)

		if resp == nil || resp.Links == nil || resp.Links.IsLastPage() {
			return all, nil
		}
		current, err := resp.Links.CurrentPage()
		if err != nil {
			return nil, err
		}
		opt.Page = current + 1
	}
}

// Account fetches the authenticated account, used to validate tokens.
func Account(ctx context.Context, client *godo.Client) (*godo.Account, error) {
	acct, _, err := client.Account.Get(ctx)
	if err != nil {
		return nil, err
	}
	return acct, nil
}
