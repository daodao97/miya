package uiruntime

import (
	"image"

	"github.com/egoist/mygo/ui"
)

// UpdateStreamBitmap reuses the GPU texture across frames. Call it on the UI
// thread after decoding the JPEG off-thread.
func UpdateStreamBitmap(old ui.ImageSource, decoded image.Image) *ui.Bitmap {
	bitmap, _ := old.(*ui.Bitmap)
	if yuv, ok := decoded.(*image.YCbCr); ok && yuv.SubsampleRatio == image.YCbCrSubsampleRatio420 {
		if bitmap == nil {
			bitmap = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		bitmap.UpdateYUV(&ui.YUVImage{
			Format: ui.I420, Width: yuv.Rect.Dx(), Height: yuv.Rect.Dy(),
			Planes: [3][]byte{yuv.Y, yuv.Cb, yuv.Cr}, Strides: [3]int{yuv.YStride, yuv.CStride, yuv.CStride},
			Matrix: ui.BT601, FullRange: true,
		})
		return bitmap
	}
	if bitmap == nil {
		return ui.NewBitmap(decoded)
	}
	bitmap.Update(decoded)
	return bitmap
}
