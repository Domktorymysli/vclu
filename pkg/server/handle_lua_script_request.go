package server

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
)

// handleLuaScriptRequest handles a Lua script execution request
func (s *Server) handleLuaScriptRequest(req *commands.LuaScriptRequest, requestKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	startTime := time.Now()

	var returnValue string
	var hasError bool
	if s.luaEngine != nil {
		// A script from a CLU or Object Manager: its changes come from that device
		result, err := s.luaEngine.ExecuteAs("grenton", s.senderActor(remoteAddr), "", req.Script)
		if err != nil {
			log.Printf("[LUA] %s -> error: %v", req.Script, err)
			returnValue = fmt.Sprintf("error: %v", err)
			hasError = true
		} else {
			log.Printf("[LUA] %s -> %s", req.Script, result)
			returnValue = result
		}
	} else {
		returnValue = "ok"
		log.Printf("[LUA] %s -> ok (no engine)", req.Script)
	}

	// Record metrics
	if s.metrics != nil {
		s.metrics.RecordCommand(true, hasError)
		s.metrics.RecordLuaExecution(time.Since(startTime).Nanoseconds(), hasError)
	}

	response := &commands.LuaScriptResponse{
		IPAddress:   s.localIP,
		SessionID:   req.SessionID,
		ReturnValue: returnValue,
	}

	responseData := response.AsBytes()

	// Encrypt response with the same key used for request
	encryptedResponse, err := requestKey.Encrypt(responseData)
	if err != nil {
		log.Printf("[LUA] failed to encrypt response: %v", err)
		return
	}

	// Send response
	_, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr)
	if err != nil {
		log.Printf("[LUA] failed to send response to %s: %v", remoteAddr, err)
		return
	}
}

// senderActor names the device that sent a script: device:<CLU name> as in
// CLU reports when the address belongs to a known CLU, else device:<ip>
// (e.g. Object Manager).
func (s *Server) senderActor(addr *net.UDPAddr) string {
	if addr == nil {
		return "device:unknown"
	}
	ip := addr.IP.String()
	senderNames.mu.Lock()
	defer senderNames.mu.Unlock()
	if time.Since(senderNames.at) > time.Minute {
		senderNames.byIP = map[string]string{}
		if clus, err := s.GetRegisteredCLUs(); err == nil {
			for _, c := range clus {
				if strings.HasPrefix(c.Name, "CLU") {
					senderNames.byIP[c.IP] = c.Name
				}
			}
		}
		senderNames.at = time.Now()
	}
	if name, ok := senderNames.byIP[ip]; ok {
		return "device:" + name
	}
	return "device:" + ip
}

// senderNames caches CLU names by IP for a minute: a script from a CLU must
// not scan the Lua globals every time.
var senderNames struct {
	mu   sync.Mutex
	at   time.Time
	byIP map[string]string
}
