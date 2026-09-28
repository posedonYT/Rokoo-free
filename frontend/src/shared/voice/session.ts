"use client";

import { voiceBase } from "@/shared/api/transport";
import { voiceHttpBase } from "@/shared/voice/state";

type Handlers = {
  onLocal: (stream: MediaStream) => void;
  onRemote: (stream: MediaStream) => void;
  onRemoteGone: (streamId: string) => void;
  onPeerJoined: (userId: string) => void;
  onPeerLeft: (userId: string) => void;
};

export type CallHandle = {
  mute: (on: boolean) => void;
  camera: (on: boolean) => void;
  hangup: () => void;
  setLayer: (track: string, layer: VideoLayer) => void;
  pauseVideo: (track: string, paused: boolean) => void;
};

export type IceServer = { urls: string[]; username?: string; credential?: string };
export type VideoLayer = "q" | "h" | "f";

export function videoEncodings() {
  return [
    { rid: "q", maxBitrate: 150_000, scaleResolutionDownBy: 4 },
    { rid: "h", maxBitrate: 500_000, scaleResolutionDownBy: 2 },
    { rid: "f", maxBitrate: 1_500_000, scaleResolutionDownBy: 1 },
  ];
}

export function layerForTile(width: number): VideoLayer {
  if (width >= 640) return "f";
  if (width >= 280) return "h";
  return "q";
}

export function shouldRestartIce(state: string) {
  return state === "disconnected" || state === "failed";
}

export async function resolveVoiceURL(token: string, roomId: string, kind: "dm" | "guild", base = voiceBase): Promise<string> {
  const http = voiceHttpBase(base);
  const url = new URL("/rooms/resolve", http);
  url.searchParams.set("kind", kind);
  url.searchParams.set("id", roomId);
  url.searchParams.set("token", token);
  const res = await fetch(url);
  if (!res.ok) throw new Error("voice resolve failed");
  const body = (await res.json()) as { ws_url?: string };
  if (!body.ws_url) return base;
  return body.ws_url.endsWith("/ws") ? body.ws_url : `${body.ws_url.replace(/\/$/, "")}/ws`;
}

export async function startCall(token: string, roomId: string, kind: "dm" | "guild", video: boolean, handlers: Handlers): Promise<CallHandle> {
  const local = await navigator.mediaDevices.getUserMedia({ audio: true, video });
  handlers.onLocal(local);
  let stopped = false;
  let pc = createPeer([]);
  let ws: WebSocket | null = null;
  const bind = async (endpoint: string) => {
    pc = createPeer([]);
    attachLocal(pc, local, video);
    const known = new Set<string>();
    pc.ontrack = (event) => {
      const stream = event.streams[0] ?? new MediaStream([event.track]);
      if (!event.streams[0] && !stream.getTracks().includes(event.track)) {
        stream.addTrack(event.track);
      }
      if (known.has(stream.id)) return;
      known.add(stream.id);
      stream.onremovetrack = () => {
        if (stream.getTracks().length > 0) return;
        known.delete(stream.id);
        handlers.onRemoteGone(stream.id);
      };
      handlers.onRemote(stream);
    };
    const socket = new WebSocket(`${endpoint}${endpoint.includes("?") ? "&" : "?"}token=${encodeURIComponent(token)}`);
    ws = socket;
    const send = (body: unknown) => {
      if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(body));
    };
    pc.onicecandidate = (event) => {
      if (!event.candidate) return;
      send({
        type: "candidate",
        candidate: {
          candidate: event.candidate.candidate,
          sdpMid: event.candidate.sdpMid,
          sdpMLineIndex: event.candidate.sdpMLineIndex,
        },
      });
    };
    let configured = false;
    let joined = false;
    let offered = false;
    const offer = async (restart = false) => {
      if (!restart) {
        if (!configured || !joined || offered || stopped) return;
        offered = true;
      }
      const desc = await pc.createOffer(restart ? { iceRestart: true } : undefined);
      await pc.setLocalDescription(desc);
      send({ type: "offer", sdp: desc.sdp });
    };
    pc.oniceconnectionstatechange = () => {
      if (!shouldRestartIce(pc.iceConnectionState) || stopped) return;
      void offer(true);
    };
    await new Promise<void>((resolve, reject) => {
      socket.onopen = () => resolve();
      socket.onerror = () => reject(new Error("voice socket failed"));
    });
    send({ type: "join", room_id: roomId, kind });
    socket.onmessage = async (ev) => {
      const msg = JSON.parse(String(ev.data)) as {
        type: string;
        sdp?: string;
        user_id?: string;
        ice_servers?: IceServer[];
        candidate?: { candidate: string; sdpMid?: string; sdpMLineIndex?: number };
      };
      switch (msg.type) {
        case "config":
          if (msg.ice_servers?.length) pc.setConfiguration({ iceServers: msg.ice_servers });
          configured = true;
          await offer();
          return;
        case "joined":
          joined = true;
          await offer();
          return;
        case "answer":
          if (msg.sdp) await pc.setRemoteDescription({ type: "answer", sdp: msg.sdp });
          return;
        case "offer":
          if (!msg.sdp) return;
          await pc.setRemoteDescription({ type: "offer", sdp: msg.sdp });
          await pc.setLocalDescription(await pc.createAnswer());
          send({ type: "answer", sdp: pc.localDescription?.sdp });
          return;
        case "candidate":
          if (msg.candidate) await pc.addIceCandidate(msg.candidate);
          return;
        case "peer_joined":
          if (msg.user_id) handlers.onPeerJoined(msg.user_id);
          return;
        case "peer_left":
          if (msg.user_id) handlers.onPeerLeft(msg.user_id);
          return;
        case "reconnect":
          if (stopped) return;
          pc.close();
          ws?.close();
          await bind(await resolveVoiceURL(token, roomId, kind));
          return;
        case "ring_timeout":
        case "declined":
          stopped = true;
          local.getTracks().forEach((track) => track.stop());
          pc.close();
          ws?.close();
          return;
      }
    };
    return { send };
  };
  let endpoint = voiceBase;
  try {
    endpoint = await resolveVoiceURL(token, roomId, kind);
  } catch {
    endpoint = voiceBase;
  }
  let api: { send: (body: unknown) => void };
  try {
    api = await bind(endpoint);
  } catch (err) {
    stopped = true;
    local.getTracks().forEach((track) => track.stop());
    pc.close();
    throw err;
  }
  return {
    mute(on) {
      local.getAudioTracks().forEach((track) => {
        track.enabled = !on;
      });
    },
    camera(on) {
      local.getVideoTracks().forEach((track) => {
        track.enabled = on;
      });
    },
    setLayer(track, layer) {
      api.send({ type: "set_layer", track, layer });
    },
    pauseVideo(track, paused) {
      api.send({ type: "pause_video", track, paused });
    },
    hangup() {
      stopped = true;
      local.getTracks().forEach((track) => track.stop());
      pc.close();
      ws?.close();
    },
  };
}

function createPeer(iceServers: IceServer[]) {
  return new RTCPeerConnection({ iceServers });
}

function attachLocal(pc: RTCPeerConnection, local: MediaStream, video: boolean) {
  for (const track of local.getAudioTracks()) pc.addTrack(track, local);
  for (const track of local.getVideoTracks()) {
    if (!video) {
      track.enabled = false;
    }
    const transceiver = pc.addTransceiver(track, { direction: "sendonly", streams: [local], sendEncodings: videoEncodings() });
    void transceiver;
  }
}
