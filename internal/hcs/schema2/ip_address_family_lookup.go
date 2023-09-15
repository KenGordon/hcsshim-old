package hcsschema

func (f IpAddressFamily) Int64() int64 {
	return map[IpAddressFamily]int64{
		IpAddressFamily_NONE: 0x00,
		IpAddressFamily_IPV4: 0x01,
		IpAddressFamily_IPV6: 0x02,
	}[f]
}
