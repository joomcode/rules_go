package bzltestutil

import (
	"bytes"
	"fmt"
)

type Output struct {
	prefix []byte
	suffix []byte
	limit  uint64
	offset uint64
	size   uint64
}

func NewOutput(limit uint64) *Output {
	return &Output{
		prefix: make([]byte, 0, limit),
		limit:  limit,
	}
}

func (o *Output) WriteString(d string) {
	o.Write([]byte(d))
}

func (o *Output) Write(d []byte) {
	l := uint64(len(d))
	if o.size < o.limit {
		s := o.limit - o.size
		if l := uint64(len(d)); s > l {
			s = l
		}
		o.prefix = append(o.prefix, d[0:s]...)
		o.size += s
		d = d[s:]
		l -= s
	}
	if l == 0 {
		return
	}
	if l > o.limit {
		skip := l - o.limit
		o.size += skip
		o.offset = 0
		d = d[skip:]
		l -= skip
	}
	if o.suffix == nil {
		o.suffix = make([]byte, o.limit)
	}
	if l+o.offset > o.limit {
		s := o.limit - o.offset
		copy(o.suffix[o.offset:o.limit], d[0:s])
		o.offset = 0
		o.size += s
		d = d[s:]
		l -= s
	}
	copy(o.suffix[o.offset:o.offset+l], d)
	o.offset += l
	o.size += l
	if o.offset == o.limit {
		o.offset = 0
	}
}

func (o *Output) MarshalText() ([]byte, error) {
	return o.Bytes(), nil
}

func (o *Output) Bytes() []byte {
	suffix := o.getSuffix()
	if suffix == nil {
		return o.prefix
	}
	if o.size <= uint64(len(o.prefix)+len(o.suffix)) {
		result := make([]byte, 0, o.size)
		result = append(result, o.prefix...)
		result = append(result, suffix...)
		return result
	}
	return o.joinedOutput(o.prefix, suffix)
}

func (o *Output) String() string {
	return string(o.Bytes())
}

func (o *Output) joinedOutput(prefix, suffix []byte) []byte {
	if i := bytes.LastIndexByte(prefix, '\n'); i > len(prefix)/2 {
		prefix = prefix[:i+1]
	}
	if i := bytes.IndexByte(suffix, '\n'); i < len(suffix)/2 {
		suffix = suffix[i:]
	}
	message := fmt.Sprintf("\n... Too big output (total: %d, skipped: %d) ...\n", o.size, o.size-uint64(len(prefix)+len(suffix)))
	result := make([]byte, 0, len(prefix)+len(suffix)+len(message))
	result = append(result, prefix...)
	result = append(result, []byte(message)...)
	result = append(result, suffix...)
	return result
}

func (o *Output) getSuffix() []byte {
	if o.suffix == nil {
		return nil
	}
	if o.size < uint64(len(o.prefix)+len(o.suffix)) {
		return o.suffix[:o.offset]
	}
	if o.offset == 0 {
		return o.suffix
	}
	result := make([]byte, 0, len(o.suffix))
	result = append(result, o.suffix[o.offset:]...)
	result = append(result, o.suffix[:o.offset]...)
	return result
}
