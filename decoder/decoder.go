package decoder

import "C"
import (
	"errors"
	"github.com/MikolajMGT/h264decoder/rgb"
	"github.com/ailumiyana/goav-incr/goav/avcodec"
	"github.com/ailumiyana/goav-incr/goav/avutil"
	"github.com/ailumiyana/goav-incr/goav/swscale"
	"image"
	"unsafe"
)

type PixelFormat int

const (
	PixelFormatRGB = avcodec.AV_PIX_FMT_RGB24
	PixelFormatBGR = av_PIX_FMT_BGR24
)

type Compression int

const (
	H264 = Compression(avcodec.AV_CODEC_ID_H264)
	H265 = Compression(avcodec.AV_CODEC_ID_H265)
)

// our avcodec wrapper doesn't have this constant
const av_PIX_FMT_BGR24 = 3

type Decoder struct {
	context   *avcodec.Context
	parser    *avcodec.ParserContext
	frame     *avutil.Frame
	pkt       *avcodec.Packet
	converter *converter
}

// Frame represents decoded frame from H.264/H.265 stream
// Data field will contain bitmap data in the pixel format specified in the decoder
type Frame struct {
	Data                  []byte
	Width, Height, Stride int
}

// New creates new Decoder
// It accepts expected pixel format for the output which
func New(pxlFmt PixelFormat, cpr Compression) (*Decoder, error) {
	avcodec.AvcodecRegisterAll()
	codec := avcodec.AvcodecFindDecoder(avcodec.CodecId(cpr))
	if codec == nil {
		return nil, errors.New("cannot find decoder")
	}
	context := codec.AvcodecAllocContext3()
	if context == nil {
		return nil, errors.New("cannot allocate context")
	}

	if context.AvcodecOpen2(codec, nil) < 0 {
		return nil, errors.New("cannot open content")
	}
	parser := avcodec.AvParserInit(int(cpr))
	if parser == nil {
		return nil, errors.New("cannot init parser")
	}
	frame := avutil.AvFrameAlloc()
	if frame == nil {
		return nil, errors.New("cannot allocate frame")
	}
	pkt := avcodec.AvPacketAlloc()
	if pkt == nil {
		return nil, errors.New("cannot allocate packet")
	}
	pkt.AvInitPacket()
	pkt.SetFlags(pkt.Flags() | avcodec.AV_CODEC_FLAG_TRUNCATED)

	if cpr != H264 && cpr != H265 {
		return nil, errors.New("unsupported compression")
	}

	var converterPxlFmt swscale.PixelFormat
	switch pxlFmt {
	case PixelFormatRGB:
		converterPxlFmt = avcodec.AV_PIX_FMT_RGB24
	case PixelFormatBGR:
		converterPxlFmt = av_PIX_FMT_BGR24
	default:
		return nil, errors.New("unsupported pixel format")
	}

	converter, err := newConverter(converterPxlFmt)
	if err != nil {
		return nil, err
	}

	h := &Decoder{
		context:   context,
		parser:    parser,
		frame:     frame,
		pkt:       pkt,
		converter: converter,
	}

	return h, nil
}

// Decode tries to parse the input data and return list of frames
// If input data doesn't contain any H.264/H.265 frames the list will be empty
func (h *Decoder) Decode(data []byte) ([]*Frame, error) {
	var frames []*Frame

	for len(data) > 0 {
		frame, nread, isFrameAvailable, err := h.decodeFrameImpl(data)

		if err != nil && nread < 0 {
			return nil, err
		}

		if isFrameAvailable && frame != nil {
			frames = append(frames, frame)
		}

		data = data[nread:]
	}

	return frames, nil
}

// Close free ups memory used for decoder structures
// It needs to be called to prevent memory leaks
func (h *Decoder) Close() {
	h.converter.Close()

	avcodec.AvParserClose(h.parser)
	h.context.AvcodecClose()
	avutil.AvFree(unsafe.Pointer(h.context))
	avutil.AvFrameFree(h.frame)
	h.pkt.AvFreePacket()
}

// ToRGBA converts the frame into image.RGBA
// The returned image share the same memory as the frame
func (f *Frame) ToRGB() *rgb.Image {
	rect := image.Rect(0, 0, f.Width, f.Height)
	return &rgb.Image{
		Pix:    f.Data,
		Stride: f.Stride,
		Rect:   rect,
	}
}

func (h *Decoder) parse(data []byte, bs int) int {
	return h.context.AvParserParse2(
		h.parser,
		h.pkt,
		data,
		bs,
		0, 0, avcodec.AV_NOPTS_VALUE,
	)
}

func (h *Decoder) isFrameAvailable() bool {
	return h.pkt.Size() > 0
}

func (h *Decoder) decodeFrame() (*avutil.Frame, error) {
	gotPicture := 0
	nread := h.context.AvcodecDecodeVideo2((*avcodec.Frame)(unsafe.Pointer(h.frame)), &gotPicture, h.pkt)
	if nread < 0 || gotPicture == 0 {
		return nil, errors.New("error decoding frame")
	}

	return h.frame, nil
}

func (h *Decoder) decodeFrameImpl(data []byte) (*Frame, int, bool, error) {
	size := len(data)
	nread := h.parse(data, size)

	if !h.isFrameAvailable() {
		return nil, nread, false, nil
	}

	frame, err := h.decodeFrame()
	if err != nil {
		return nil, nread, true, err
	}

	width, height := h.context.Width(), h.context.Height()
	bufferSize := uintptr(h.converter.PredictSize(width, height))
	buffer := (*uint8)(avutil.AvMalloc(bufferSize))
	defer avutil.AvFree(unsafe.Pointer(buffer))
	rgbframe, err := h.converter.Convert(h.context, frame, buffer)

	if err != nil {
		return nil, nread, true, err
	}

	return newFrame(rgbframe), nread, true, nil
}

func newFrame(frame *avutil.Frame) *Frame {
	w, h, linesize := frame.Width(), frame.Height(), avutil.Linesize(frame)

	return &Frame{
		Data:   frameData(frame),
		Width:  w,
		Height: h,
		Stride: int(linesize[0]),
	}
}

func frameData(frame *avutil.Frame) []byte {
	h, linesize, data := frame.Height(), avutil.Linesize(frame), avutil.Data(frame)
	size := int(linesize[0]) * h

	return C.GoBytes(unsafe.Pointer(data[0]), C.int(size))
}

type converter struct {
	framergb *avutil.Frame
	context  *swscale.Context
	pixFmt   swscale.PixelFormat
}

func newConverter(pixelFormat swscale.PixelFormat) (*converter, error) {
	c := &converter{
		pixFmt: pixelFormat,
	}

	c.framergb = avutil.AvFrameAlloc()
	if c.framergb == nil {
		return nil, errors.New("cannot allocate frame")
	}

	return c, nil
}

func (c *converter) Close() {
	swscale.SwsFreecontext(c.context)
	avutil.AvFrameFree(c.framergb)
}

func (c *converter) Convert(context *avcodec.Context, frame *avutil.Frame, out_rgb *uint8) (*avutil.Frame, error) {
	w, h, pixFmt := context.Width(), context.Height(), context.PixFmt()

	swsCtx := c.context

	if c.context == nil {
		swsCtx = swscale.SwsGetcontext(
			w,
			h,
			(swscale.PixelFormat)(pixFmt),
			w,
			h,
			c.pixFmt,
			avcodec.SWS_BILINEAR,
			nil,
			nil,
			nil,
		)
	} else {
		swsCtx = swscale.SwsGetcachedcontext(
			swsCtx,
			w,
			h,
			(swscale.PixelFormat)(pixFmt),
			w,
			h,
			c.pixFmt,
			avcodec.SWS_BILINEAR,
			nil,
			nil,
			nil,
		)
	}

	if context == nil {
		return nil, errors.New("cannot allocate context")
	}

	err := avutil.AvSetFrame(c.framergb, w, h, int(c.pixFmt))
	if err != nil {
		return nil, err
	}

	avp := (*avcodec.Picture)(unsafe.Pointer(c.framergb))
	avp.AvpictureFill(
		(*uint8)(out_rgb),
		(avcodec.PixelFormat)(c.pixFmt),
		w, h,
	)
	swscale.SwsScale2(swsCtx, avutil.Data(frame),
		avutil.Linesize(frame), 0, h,
		avutil.Data(c.framergb), avutil.Linesize(c.framergb))

	return c.framergb, err
}

func (c *converter) PredictSize(w, h int) int {
	avp := (*avcodec.Picture)(unsafe.Pointer(c.framergb))
	return avp.AvpictureFill(nil, (avcodec.PixelFormat)(c.pixFmt), w, h)
}
