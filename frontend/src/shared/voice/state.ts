export type VoiceRooms = Record<string, string[]>;

export type IncomingCall = { roomId: string; fromId: string };

export function parseParticipants(raw?: string): string[] {
  return raw ? raw.split(",").filter(Boolean) : [];
}

export function applyVoiceState(rooms: VoiceRooms, roomId: string, participants: string[]): VoiceRooms {
  const next = { ...rooms };
  if (participants.length) next[roomId] = participants;
  else delete next[roomId];
  return next;
}

export function othersInCall(rooms: VoiceRooms, roomId: string, me: string): string[] {
  return (rooms[roomId] ?? []).filter((id) => id !== me);
}

export function shouldDismissIncoming(incoming: IncomingCall | null, roomId: string, participants: string[], me: string): boolean {
  if (!incoming || incoming.roomId !== roomId) return false;
  return participants.includes(me) || !participants.includes(incoming.fromId);
}

export function voiceHttpBase(wsUrl: string): string {
  return wsUrl.replace(/^ws/, "http").replace(/\/ws$/, "");
}
