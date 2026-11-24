package manage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ctPairKeySize = 32
const ctExtraChunk = 700
const ctExtraParallel = "parallel-v1"
const ctExtraParallelSetup = "parallel-setup-v1"
const ctExtraSetupWorkers = 4

type ctExtraJob struct {
	ID     uint64 `json:"i"`
	Action string `json:"a"`
	Mode   string `json:"m,omitempty"`
	Name   string `json:"n,omitempty"`
	Token  string `json:"t,omitempty"`
}

type ctExtraReply struct {
	ID    uint64 `json:"i"`
	Port  int    `json:"p,omitempty"`
	Error string `json:"e,omitempty"`
}

func ctExtraCipher(key string) (cipher.AEAD, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != ctPairKeySize {
		return nil, errors.New("invalid transport-test pairing key")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func ctExtraSeal(aead cipher.AEAD, label string, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, data, []byte(label))), nil
}

func ctExtraOpen(aead cipher.AEAD, label, value string, out any) error {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return errors.New("invalid transport-test message")
	}
	nonce := data[:aead.NonceSize()]
	raw, err := aead.Open(nil, nonce, data[aead.NonceSize():], []byte(label))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Each worker has one outstanding job. Its encrypted reply is keyed by job
// ID, so a retry cannot rerun the job or acknowledge a different worker.
type ctExtraControl struct {
	mu       sync.Mutex
	key      cipher.AEAD
	ready    bool
	parallel bool
	workers  int
	sequence uint64
	jobs     [ctExtraSetupWorkers]ctExtraPending
}

type ctExtraPending struct {
	id    uint64
	body  string
	reply chan ctExtraReply
}

func (c *ctExtraControl) answer(command, value string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch command {
	case "extra-ready":
		var capability string
		if ctExtraOpen(c.key, "ready", value, &capability) == nil {
			switch capability {
			case ctExtraParallelSetup:
				c.workers = ctExtraSetupWorkers
				c.ready, c.parallel = true, true
				return "ok"
			case ctExtraParallel:
				c.ready, c.parallel = true, true
				return "ok"
			}
		}
		var ready bool
		if ctExtraOpen(c.key, "ready", value, &ready) == nil && ready {
			c.ready = true
			return "ok"
		}
	case "extra-job":
		worker := 0
		if lane, offset, ok := strings.Cut(value, ":"); ok {
			index, err := strconv.Atoi(lane)
			if err != nil || index < 0 || index >= c.workers || index >= len(c.jobs) {
				return ""
			}
			worker, value = index, offset
		}
		offset, err := strconv.Atoi(value)
		if err != nil || offset < 0 {
			return ""
		}
		job := c.jobs[worker]
		if job.body == "" || offset >= len(job.body) {
			return "wait"
		}
		end := min(offset+ctExtraChunk, len(job.body))
		return fmt.Sprintf("job %d:%d:%s", job.id, len(job.body), job.body[offset:end])
	case "extra-reply":
		var r ctExtraReply
		if ctExtraOpen(c.key, "reply", value, &r) != nil || r.ID == 0 {
			return ""
		}
		for _, job := range c.jobs {
			if job.id == r.ID {
				select {
				case job.reply <- r:
				default:
				}
				return "ok"
			}
		}
	}
	return ""
}

func (c *ctExtraControl) parallelReady() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.parallel
}

func (c *ctExtraControl) setupWorkers() int {
	if c == nil {
		return 1
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return max(1, c.workers)
}

func (c *ctExtraControl) exchange(ctx context.Context, worker int, job ctExtraJob) (ctExtraReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	c.mu.Lock()
	if worker < 0 || worker >= max(1, c.workers) || worker >= len(c.jobs) || c.jobs[worker].reply != nil {
		c.mu.Unlock()
		return ctExtraReply{}, errors.New("temporary transport worker is unavailable")
	}
	c.sequence++
	job.ID = c.sequence
	body, err := ctExtraSeal(c.key, "job", job)
	if err != nil {
		c.mu.Unlock()
		return ctExtraReply{}, err
	}
	replies := make(chan ctExtraReply, 1)
	c.jobs[worker] = ctExtraPending{id: job.ID, body: body, reply: replies}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.jobs[worker] = ctExtraPending{}
		c.mu.Unlock()
	}()
	select {
	case r := <-replies:
		if r.Error != "" {
			return r, fmt.Errorf("Kharej setup: %s", r.Error)
		}
		return r, nil
	case <-ctx.Done():
		return ctExtraReply{}, fmt.Errorf("Kharej did not complete %s on the test coordinator: %w", job.Action, ctx.Err())
	}
}

func ctExtraReadJob(ctx context.Context, link ConnTestLink, key cipher.AEAD, worker int) (ctExtraJob, error) {
	var job ctExtraJob
	var encoded strings.Builder
	var sequence string
	for total := 1; encoded.Len() < total; {
		offset := strconv.Itoa(encoded.Len())
		if worker > 0 {
			offset = strconv.Itoa(worker) + ":" + offset
		}
		r, err := ctAskContext(ctx, link.Host, link.Coord, "extra-job "+link.Tok+" "+offset)
		if err != nil {
			return job, err
		}
		if r == "stopped" || r == "wait" {
			return job, nil
		}
		wire, ok := strings.CutPrefix(r, "job ")
		parts := strings.SplitN(wire, ":", 3)
		if !ok || len(parts) != 3 {
			return job, errors.New("invalid transport-test job")
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n <= encoded.Len() || n > 16000 || len(parts[2]) == 0 || encoded.Len()+len(parts[2]) > n || (sequence != "" && (sequence != parts[0] || total != n)) {
			return job, errors.New("invalid transport-test job length")
		}
		sequence, total = parts[0], n
		encoded.WriteString(parts[2])
	}
	if err := ctExtraOpen(key, "job", encoded.String(), &job); err != nil {
		return ctExtraJob{}, err
	}
	if strconv.FormatUint(job.ID, 10) != sequence {
		return ctExtraJob{}, errors.New("transport-test job ID does not match")
	}
	return job, nil
}
