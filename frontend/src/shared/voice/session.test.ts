import { layerForTile, shouldRestartIce, videoEncodings } from "@/shared/voice/session";
import { describe, expect, it } from "vitest";

describe("voice session helpers", () => {
  it("builds three simulcast layers", () => {
    const layers = videoEncodings();
    expect(layers.map((layer) => layer.rid)).toEqual(["q", "h", "f"]);
    expect(layers[0].scaleResolutionDownBy).toBe(4);
    expect(layers[2].scaleResolutionDownBy).toBe(1);
  });

  it("picks a layer from tile width", () => {
    expect(layerForTile(120)).toBe("q");
    expect(layerForTile(320)).toBe("h");
    expect(layerForTile(900)).toBe("f");
  });

  it("restarts ice after a disconnect", () => {
    expect(shouldRestartIce("disconnected")).toBe(true);
    expect(shouldRestartIce("failed")).toBe(true);
    expect(shouldRestartIce("connected")).toBe(false);
  });
});
