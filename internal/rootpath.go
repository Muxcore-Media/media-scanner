package internal

import (
	"context"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
)

func (m *Module) listRegisteredRoots(ctx context.Context) ([]*rootsv1.RootFolder, bool) {
	if m.mc == nil {
		return nil, false
	}
	addr, err := m.findCapabilityAddr(ctx, "media.roots")
	if err != nil {
		return nil, false
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, false
	}
	defer func() { _ = conn.Close() }()
	cli := rootsv1.NewRootFolderServiceClient(conn)
	resp, err := cli.ListRoots(ctx, &rootsv1.ListRootsRequest{})
	if err != nil {
		return nil, false
	}
	return resp.GetRoots(), true
}

func (m *Module) namingTemplateForLibPath(libPath string) string {
	root := strings.TrimSpace(libPath)
	if root == "" {
		root = m.libraryRoot
	}
	root = filepath.Clean(root)
	if root == "" || root == "." {
		return ""
	}
	ctx := context.Background()
	roots, ok := m.listRegisteredRoots(ctx)
	if !ok {
		return ""
	}
	for _, r := range roots {
		if filepath.Clean(r.GetPath()) == root {
			return strings.TrimSpace(r.GetNamingTemplateId())
		}
	}
	return ""
}
