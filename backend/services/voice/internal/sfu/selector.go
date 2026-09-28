package sfu

type Layer string

const (
	LayerQ    Layer = "q"
	LayerH    Layer = "h"
	LayerF    Layer = "f"
	LayerNone Layer = ""
)

var layerBitrate = map[Layer]int{
	LayerQ: 200_000,
	LayerH: 800_000,
	LayerF: 2_500_000,
}

var layerRank = map[Layer]int{
	LayerQ: 1,
	LayerH: 2,
	LayerF: 3,
}

func Choose(bweBps int, preferred Layer, available []Layer) Layer {
	have := map[Layer]bool{}
	for _, layer := range available {
		if layer == LayerQ || layer == LayerH || layer == LayerF {
			have[layer] = true
		}
	}
	if len(have) == 0 {
		return LayerNone
	}
	if preferred != LayerNone && have[preferred] && fits(bweBps, preferred) {
		return preferred
	}
	best := LayerNone
	for _, layer := range []Layer{LayerF, LayerH, LayerQ} {
		if !have[layer] || !fits(bweBps, layer) {
			continue
		}
		if best == LayerNone || layerRank[layer] > layerRank[best] {
			best = layer
		}
	}
	if best != LayerNone {
		return best
	}
	for _, layer := range []Layer{LayerQ, LayerH, LayerF} {
		if have[layer] {
			return layer
		}
	}
	return LayerNone
}

func fits(bweBps int, layer Layer) bool {
	if bweBps <= 0 {
		return layer == LayerQ
	}
	need, ok := layerBitrate[layer]
	if !ok {
		return false
	}
	return bweBps >= need
}
