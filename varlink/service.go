package varlink

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rossedman/sdvarlink/idl"
)

// Info is the reply to org.varlink.service.GetInfo.
type Info struct {
	Vendor     string   `json:"vendor"`
	Product    string   `json:"product"`
	Version    string   `json:"version"`
	URL        string   `json:"url"`
	Interfaces []string `json:"interfaces"`
}

// GetInfo asks the service for its vendor details and the interfaces it
// implements.
func GetInfo(ctx context.Context, c Caller) (*Info, error) {
	var info Info
	if err := c.Call(ctx, "org.varlink.service.GetInfo", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetInterfaceDescription returns the IDL text of one interface the service
// implements. Parse it with the idl package.
func GetInterfaceDescription(ctx context.Context, c Caller, iface string) (string, error) {
	var out struct {
		Description string `json:"description"`
	}
	in := struct {
		Interface string `json:"interface"`
	}{iface}
	if err := c.Call(ctx, "org.varlink.service.GetInterfaceDescription", in, &out); err != nil {
		return "", err
	}
	return out.Description, nil
}

// SplitMethod splits a qualified method name such as
// "io.systemd.Resolve.ResolveHostname" into interface and method.
func SplitMethod(qualified string) (iface, method string, err error) {
	i := strings.LastIndexByte(qualified, '.')
	if i <= 0 || i == len(qualified)-1 {
		return "", "", fmt.Errorf("varlink: %q is not a qualified method name", qualified)
	}
	return qualified[:i], qualified[i+1:], nil
}

// Supports reports whether the service implements the qualified method, by
// reading its interface description. A missing interface is not an error:
// it reports false. Use it to feature-detect methods added in newer systemd
// releases before calling them.
func Supports(ctx context.Context, c Caller, qualified string) (bool, error) {
	iface, method, err := SplitMethod(qualified)
	if err != nil {
		return false, err
	}
	desc, err := GetInterfaceDescription(ctx, c, iface)
	if errors.Is(err, ErrInterfaceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	parsed, err := idl.Parse(desc)
	if err != nil {
		return false, fmt.Errorf("varlink: parse %s description: %w", iface, err)
	}
	return parsed.Method(method) != nil, nil
}
