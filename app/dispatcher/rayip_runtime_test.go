package dispatcher

import (
	"testing"

	"github.com/xtls/xray-core/app/rayipruntime"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport"
)

func TestRayIPRuntimeLinkRecordsBothDirections(t *testing.T) {
	manager := rayipruntime.NewManager()
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "acct-1", Priority: 1})
	downlink := &buf.MultiBufferContainer{}

	link, release, err := wrapRayIPRuntimeLink("acct-1", manager, &transport.Link{
		Reader: buf.NewReader(&buf.MultiBufferContainer{
			MultiBuffer: buf.MergeBytes(nil, []byte("uplink")),
		}),
		Writer: downlink,
	})
	if err != nil {
		t.Fatalf("wrapRayIPRuntimeLink() error = %v", err)
	}
	defer release()

	mb, err := link.Reader.ReadMultiBuffer()
	if err != nil {
		t.Fatalf("ReadMultiBuffer() error = %v", err)
	}
	common.Must(link.Writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("downlink"))))
	buf.ReleaseMulti(mb)

	usage := manager.Usage("acct-1")
	if usage.TxBytes != int64(len("uplink")) {
		t.Fatalf("tx bytes = %d, want %d", usage.TxBytes, len("uplink"))
	}
	if usage.RxBytes != int64(len("downlink")) {
		t.Fatalf("rx bytes = %d, want %d", usage.RxBytes, len("downlink"))
	}
	if usage.ActiveConnections != 1 {
		t.Fatalf("active connections = %d, want 1 before release", usage.ActiveConnections)
	}
	if downlink.MultiBuffer.Len() != int32(len("downlink")) {
		t.Fatalf("downlink bytes written = %d, want %d", downlink.MultiBuffer.Len(), len("downlink"))
	}
}

func TestRayIPRuntimeLinkRejectsConnectionLimit(t *testing.T) {
	manager := rayipruntime.NewManager()
	manager.SetPolicy(rayipruntime.AccountPolicy{Email: "acct-1", MaxConnections: 1, Priority: 1})

	_, release, err := wrapRayIPRuntimeLink("acct-1", manager, &transport.Link{Reader: buf.NewReader(&buf.MultiBufferContainer{}), Writer: buf.Discard})
	if err != nil {
		t.Fatalf("first wrapRayIPRuntimeLink() error = %v", err)
	}
	defer release()

	if _, _, err := wrapRayIPRuntimeLink("acct-1", manager, &transport.Link{Reader: buf.NewReader(&buf.MultiBufferContainer{}), Writer: buf.Discard}); err == nil {
		t.Fatal("second wrapRayIPRuntimeLink() error = nil, want connection limit error")
	}
}
