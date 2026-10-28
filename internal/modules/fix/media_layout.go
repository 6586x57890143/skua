package fix

import (
	"image"
	"math"
)

type galleryLayout struct {
	Width, Height int
	Tiles         []image.Rectangle
	Score         float64
}

// Search every order-preserving row partition. At ten images this is at most
// 512 candidates, so the best complete layout is inexpensive to select.
func planGallery(media []Media) (galleryLayout, bool) {
	if len(media) < 2 || len(media) > 10 {
		return galleryLayout{}, false
	}
	aspects := make([]float64, len(media))
	for i, item := range media {
		if item.Width <= 0 || item.Height <= 0 {
			return galleryLayout{}, false
		}
		aspects[i] = float64(item.Width) / float64(item.Height)
	}
	best := galleryLayout{Score: math.Inf(1)}
	var visit func(int, []int)
	visit = func(start int, rows []int) {
		if start == len(media) {
			candidate := justifiedGallery(aspects, rows)
			if candidate.Score < best.Score {
				best = candidate
			}
			return
		}
		if len(rows) == 4 {
			return
		}
		for end := start + 1; end <= min(len(media), start+5); end++ {
			visit(end, append(rows, end))
		}
	}
	visit(0, nil)
	return best, len(best.Tiles) == len(media)
}

func justifiedGallery(aspects []float64, rows []int) galleryLayout {
	heights := make([]float64, len(rows))
	total, start := 0.0, 0
	for row, end := range rows {
		sum := 0.0
		for _, aspect := range aspects[start:end] {
			sum += aspect
		}
		heights[row] = float64(sheetWidth-(end-start-1)*sheetGap) / sum
		total += heights[row]
		start = end
	}
	total += float64((len(rows) - 1) * sheetGap)
	scale := math.Min(1, float64(sheetHeight)/total)
	width := max(1, int(math.Round(float64(sheetWidth)*scale)))
	height := max(1, int(math.Round(total*scale)))
	plan := galleryLayout{Width: width, Height: height}
	y, start := 0.0, 0
	logAreas := make([]float64, 0, len(aspects))
	shortPenalty := 0.0
	for row, end := range rows {
		x := 0.0
		for i := start; i < end; i++ {
			w := heights[row] * aspects[i]
			tile := image.Rect(int(math.Round(x*scale)), int(math.Round(y*scale)),
				int(math.Round((x+w)*scale)), int(math.Round((y+heights[row])*scale)))
			tile = tile.Intersect(image.Rect(0, 0, width, height))
			if tile.Empty() {
				return galleryLayout{Score: math.Inf(1)}
			}
			plan.Tiles = append(plan.Tiles, tile)
			// Score at a typical 450px Discord card width, including narrow strips.
			shortSide := float64(min(tile.Dx(), tile.Dy())) * 450 / float64(width)
			shortfall := math.Max(0, 65-shortSide) / 65
			shortPenalty += shortfall * shortfall
			logAreas = append(logAreas, math.Log(float64(tile.Dx()*tile.Dy())))
			x += w + sheetGap
		}
		y += heights[row] + sheetGap
		start = end
	}
	mean := 0.0
	for _, area := range logAreas {
		mean += area / float64(len(logAreas))
	}
	imbalance := 0.0
	for _, area := range logAreas {
		delta := area - mean
		imbalance += delta * delta / float64(len(logAreas))
	}
	displayedHeight := float64(height) * 450 / float64(width)
	heightPenalty := (displayedHeight - 210) / 300
	plan.Score = shortPenalty*8 + imbalance*0.5 +
		heightPenalty*heightPenalty*0.3 + float64(len(rows)-1)*0.08
	return plan
}

// Preserve native gallery interactions when its estimated crop is already mild.
// The estimate is geometry-only; it never attempts to classify artwork or faces.
func compositeGallery(media []Media) bool {
	if len(media) < 2 || len(media) > 10 {
		return false
	}
	nativeAspect := 16.0 / 9
	if len(media) == 2 {
		nativeAspect = 1
	}
	if len(media) == 3 {
		nativeAspect = 1.3
	}
	loss := 0.0
	var pixels int64
	for _, item := range media {
		if item.Type != "photo" || item.Width <= 0 || item.Height <= 0 {
			return false
		}
		size := int64(item.Width) * int64(item.Height)
		pixels += size
		if item.Width > 6000 || item.Height > 6000 || size > 8<<20 || pixels > sheetMaxPixels {
			return false
		}
		aspect := float64(item.Width) / float64(item.Height)
		loss += 1 - math.Min(aspect/nativeAspect, nativeAspect/aspect)
	}
	if loss/float64(len(media)) < 0.18 {
		return false
	}
	plan, ok := planGallery(media)
	return ok && plan.Score < 2
}

func gallerySheetHeight(media []Media) int {
	plan, ok := planGallery(media)
	if !ok {
		return sheetHeight
	}
	return plan.Height
}
