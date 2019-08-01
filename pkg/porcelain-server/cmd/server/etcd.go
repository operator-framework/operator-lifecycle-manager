package server

import (
	"context"

	"go.etcd.io/etcd/embed"
	"k8s.io/klog"
)

func (o PorcelainServerOptions) startEmbeddedEtcd(ctx context.Context, dataDir string) error {
	cfg := embed.NewConfig()
	cfg.Dir = dataDir
	e, err := embed.StartEtcd(cfg)
	if err != nil {
		return err
	}

	go func() {
		defer e.Close()
		select {
		case err := <-e.Err():
			klog.Fatalf("Embedded etcd server error: %v", err)
		case <-ctx.Done():
			klog.Infof("Context canceled, shutting down embedded etcd: %v", ctx.Err())
			e.Server.Stop() // trigger a shutdown
			klog.Infof("")
		}
	}()

	select {
	case <-e.Server.ReadyNotify():
		klog.Infof("Embedded etcd ready")
	case <-ctx.Done():
		klog.Infof("Context cancelled while waiting for embedded etcd to start")
		return <-e.Err()
	}

	return nil
}
