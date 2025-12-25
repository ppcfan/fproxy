## 1. Implementation

- [x] 1.1 Add helper function `isValidIP(host string) bool` to check if a string is a valid IP address (IPv4 or IPv6)
- [x] 1.2 Add helper function `extractHost(address string) (string, error)` to extract host from `host:port` or `:port` format
- [x] 1.3 Add error variables in config package:
  - `ErrInvalidIPAddress` - when address contains a domain name instead of IP
  - `ErrClientServersDifferentIPs` - when server endpoints have different IP addresses
- [x] 1.4 Update `ParseEndpoint` to validate that the host is a valid IP address
- [x] 1.5 Add validation logic in `Config.Validate()` to check all server endpoints have the same IP
- [x] 1.6 Add unit tests for the new validation logic in `config/config_test.go`
