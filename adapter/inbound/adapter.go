package inbound

type Adapter struct {
	inboundType string
	inboundTag  string
	quotaBytes  int64
}

func NewAdapter(inboundType string, inboundTag string) Adapter {
	return Adapter{
		inboundType: inboundType,
		inboundTag:  inboundTag,
	}
}

func (a *Adapter) Type() string {
	return a.inboundType
}

func (a *Adapter) Tag() string {
	return a.inboundTag
}

func (a *Adapter) QuotaBytes() int64 {
	return a.quotaBytes
}

func (a *Adapter) SetQuotaBytes(bytes int64) {
	a.quotaBytes = bytes
}
