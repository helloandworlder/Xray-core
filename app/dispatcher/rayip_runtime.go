package dispatcher

import (
	"github.com/xtls/xray-core/app/rayipruntime"
	"github.com/xtls/xray-core/transport"
)

func wrapRayIPRuntimeLink(email string, manager *rayipruntime.Manager, link *transport.Link) (*transport.Link, func(), error) {
	return wrapRayIPRuntimeLinkWithDirections(email, manager, link, rayipruntime.DirectionEgress, rayipruntime.DirectionIngress)
}

func wrapRayIPRuntimeInboundLink(email string, manager *rayipruntime.Manager, link *transport.Link) (*transport.Link, func(), error) {
	return wrapRayIPRuntimeLinkWithDirections(email, manager, link, rayipruntime.DirectionIngress, rayipruntime.DirectionEgress)
}

func wrapRayIPRuntimeLinkWithDirections(email string, manager *rayipruntime.Manager, link *transport.Link, readerDirection rayipruntime.Direction, writerDirection rayipruntime.Direction) (*transport.Link, func(), error) {
	return rayipruntime.WrapLink(email, manager, link, readerDirection, writerDirection)
}
