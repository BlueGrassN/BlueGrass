package tunengine

import (
	"context"
	"errors"
	"net"
	"sync"

	"bluegrass/native/core/protocolconfig"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/infra/conf"
)

// Each prepared runtime owns a distinct Xray instance. The shared builder is
// the only protocol configuration authority; this layer adds physical binding.
type xrayOutbound struct {
	mu       sync.Mutex
	ctx      context.Context
	config   *conf.OutboundDetourConfig
	instance *protocolconfig.Outbound
	closed   bool
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
}

func newXrayOutbound(ctx context.Context, c *conf.OutboundDetourConfig) (*xrayOutbound, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errGateClosed
	}
	if c == nil {
		return nil, errors.New("XRAY_OUTBOUND_MISSING")
	}
	if _, e := (&conf.Config{LogConfig: &conf.LogConfig{LogLevel: "none", AccessLog: "none", ErrorLog: "none"}, OutboundConfigs: []conf.OutboundDetourConfig{*c}}).Build(); e != nil {
		return nil, errors.New("XRAY_CONFIG_INVALID")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &xrayOutbound{ctx: ctx, cancel: cancel, config: c}, nil
}
func (o *xrayOutbound) start(iface string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil {
		return errGateClosed
	}
	if o.instance != nil {
		return nil
	}
	selected := *o.config
	if selected.StreamSetting != nil {
		copy := *selected.StreamSetting
		selected.StreamSetting = &copy
	} else {
		selected.StreamSetting = &conf.StreamConfig{}
	}
	if selected.StreamSetting.SocketSettings != nil {
		copy := *selected.StreamSetting.SocketSettings
		selected.StreamSetting.SocketSettings = &copy
	} else {
		selected.StreamSetting.SocketSettings = &conf.SocketConfig{}
	}
	selected.StreamSetting.SocketSettings.Interface = iface
	instance, e := protocolconfig.StartOutbound(o.ctx, &selected)
	if e != nil {
		return e
	}
	o.instance = instance
	return nil
}
func (o *xrayOutbound) get() (*protocolconfig.Outbound, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.instance == nil || o.ctx.Err() != nil {
		return nil, errGateClosed
	}
	return o.instance, nil
}
func (o *xrayOutbound) DialContext(ctx context.Context, network string, d M.Socksaddr) (net.Conn, error) {
	v, e := o.get()
	if e != nil {
		return nil, e
	}
	// The shared wrapper owns deadline-aware streams and flow cancellation.
	// Do not cancel ctx here: Xray performs its handshake asynchronously.
	return v.DialContext(ctx, network, d.String())
}
func (o *xrayOutbound) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	v, e := o.get()
	if e != nil {
		return nil, e
	}
	return v.ListenPacket(ctx, d.String())
}
func (o *xrayOutbound) Close() error {
	o.once.Do(func() {
		o.cancel()
		o.mu.Lock()
		o.closed = true
		v := o.instance
		o.mu.Unlock()
		if v != nil {
			o.closeErr = v.Close()
		}
	})
	return o.closeErr
}
