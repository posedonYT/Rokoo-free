import { applyVoiceState, othersInCall, parseParticipants, shouldDismissIncoming, voiceHttpBase } from "@/shared/voice/state";
import { describe, expect, it } from "vitest";

describe("voice state", () => {
  it("tracks who is in a room and drops empty rooms", () => {
    let rooms = applyVoiceState({}, "dm1", parseParticipants("1,2"));
    expect(othersInCall(rooms, "dm1", "1")).toEqual(["2"]);
    rooms = applyVoiceState(rooms, "dm1", parseParticipants(""));
    expect(rooms).toEqual({});
  });

  it("dismisses an incoming call when the caller hangs up or it is answered elsewhere", () => {
    const incoming = { roomId: "dm1", fromId: "2" };
    expect(shouldDismissIncoming(incoming, "dm1", ["2"], "1")).toBe(false);
    expect(shouldDismissIncoming(incoming, "dm1", [], "1")).toBe(true);
    expect(shouldDismissIncoming(incoming, "dm1", ["2", "1"], "1")).toBe(true);
    expect(shouldDismissIncoming(incoming, "dm9", [], "1")).toBe(false);
  });

  it("derives the voice http base from the websocket url", () => {
    expect(voiceHttpBase("ws://localhost:8082/ws")).toBe("http://localhost:8082");
    expect(voiceHttpBase("wss://voice.example.com/ws")).toBe("https://voice.example.com");
  });
});
