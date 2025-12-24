# Implementation Tasks

## 1. Protocol Layer
- [x] 1.1 Update `protocol/packet.go` to use unified 8-byte header for TCP
- [x] 1.2 Update `EncodeTCP` to include sessionID
- [x] 1.3 Update `DecodeTCP` to extract sessionID

## 2. Client Changes
- [x] 2.1 Update `handleTCPTransport` to use unified session tracking
- [x] 2.2 Update `handleTCPServerResponse` to decode sessionID from TCP responses
- [x] 2.3 Share session state between TCP and UDP paths
- [x] 2.4 Update session activity from both TCP and UDP paths

## 3. Server Changes
- [x] 3.1 Update TCP request handler to decode sessionID
- [x] 3.2 Use (sessionID, seq) for TCP deduplication
- [x] 3.3 Include sessionID in TCP responses
- [x] 3.4 Fix TCP write to handle short writes

## 4. Documentation
- [x] 4.1 Update README.md with new packet format
- [x] 4.2 Create OpenSpec change proposal
- [ ] 4.3 Archive change and update spec.md (after approval)
