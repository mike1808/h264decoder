package decoder_test

import (
	"fmt"
	"github.com/mike1808/h264decoder/decoder"
	"gocv.io/x/gocv"
	"image/jpeg"
	"io"
	"os"
	"testing"
)

func BenchmarkDecoder(b *testing.B) {
	in, err := os.Open("./stream.raw")
	defer in.Close()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		d, err := decoder.New(decoder.PixelFormatRGB)
		if err != nil {
			panic(err)
		}

		buf := make([]byte, 2048)
		offset := int64(0)

		for {
			nread, err := in.ReadAt(buf, offset)
			offset += int64(nread)

			if err != nil {
				if err == io.EOF {
					return
				} else {
					b.Error(err)
				}
			}

			_, err = d.Decode(buf[:nread])
			if err != nil {
				b.Error(err)
			}
		}

		d.Close()
	}
}

func TestDecoder(t *testing.T) {
	d, err := decoder.New(decoder.PixelFormatBGR)
	if err != nil {
		panic(err)
	}

	stream, err := os.Open("./artifacts/stream.h264")
	if err != nil {
		panic(err)
	}

	window := gocv.NewWindow("H.264 decoder")

	buf := make([]byte, 2048)

	for {
		nread, err := stream.Read(buf)

		if err != nil {
			if err == io.EOF {
				return
			} else {
				t.Error(err)
			}
		}
		frames, err := d.Decode(buf[:nread])
		if err != nil {
			t.Error(err)
		}
		if len(frames) == 0 {
			t.Log("no frames")
		} else {
			for _, frame := range frames {
				img, _ := gocv.NewMatFromBytes(frame.Height, frame.Width, gocv.MatTypeCV8UC3, frame.Data)
				if img.Empty() {
					continue
				}

				window.IMShow(img)
				window.WaitKey(10)
			}

			t.Logf("found %d frames", len(frames))
		}
	}
}

func TestDecoderImage(t *testing.T) {
	d, err := decoder.New(decoder.PixelFormatRGB)
	if err != nil {
		panic(err)
	}

	stream, err := os.Open("./artifacts/stream.h264")
	if err != nil {
		panic(err)
	}

	buf := make([]byte, 2048)
	frameCounter := 0

	for {
		nread, err := stream.Read(buf)

		if err != nil {
			if err == io.EOF {
				return
			} else {
				t.Error(err)
			}
		}
		frames, err := d.Decode(buf[:nread])
		if err != nil {
			t.Error(err)
		}
		if len(frames) == 0 {
			t.Log("no frames")
		} else {
			for _, frame := range frames {
				img := frame.ToRGB()
				f, err := os.Create(fmt.Sprintf("./artifacts/frames/frame_%d.jpg", frameCounter))
				frameCounter++
				if err != nil {
					t.Fatal(err)
				}
				err = jpeg.Encode(f, img, &jpeg.Options{Quality: 90})
				if err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			t.Logf("found %d frames", len(frames))
		}
	}
}
