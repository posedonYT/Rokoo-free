"use client";

import { api, errorText, voiceBase, wsBase } from "@/shared/api/transport";
import { useAuth } from "@/shared/auth/store";
import { connectRealtime, type RealtimeEvent } from "@/shared/realtime/socket";
import { startCall, type CallHandle } from "@/shared/voice/session";
import {
  applyVoiceState,
  othersInCall,
  parseParticipants,
  shouldDismissIncoming,
  voiceHttpBase,
  type IncomingCall,
  type VoiceRooms,
} from "@/shared/voice/state";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Hash, LogIn, Mic, Phone, PhoneOff, Plus, Users, Video } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";

const RING_TIMEOUT_MS = 45_000;

type ActiveCall = {
  roomId: string;
  kind: "dm" | "guild";
  local?: MediaStream;
  remotes: MediaStream[];
  peers: string[];
  connected: boolean;
  handle?: CallHandle;
};

type Msg = {
  id: string;
  authorId: string;
  content: string;
  editedAtUnix: bigint;
  attachments: { mediaId: string; filename: string }[];
};

export function Messenger() {
  const session = useAuth((s) => s.session);
  const clear = useAuth((s) => s.clear);
  const router = useRouter();
  const params = useSearchParams();
  const qc = useQueryClient();
  const guildId = params.get("guild") ?? "";
  const channelId = params.get("channel") ?? "";
  const dmId = params.get("dm") ?? "";
  const view = params.get("view") ?? (guildId ? "guild" : "friends");
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [typing, setTyping] = useState<string>("");
  const [presence, setPresence] = useState<Record<string, boolean>>({});
  const [incoming, setIncoming] = useState<IncomingCall | null>(null);
  const [voiceRooms, setVoiceRooms] = useState<VoiceRooms>({});
  const [call, setCall] = useState<ActiveCall | null>(null);
  const callRef = useRef<ActiveCall | null>(null);
  const incomingRef = useRef<IncomingCall | null>(null);
  const notificationRef = useRef<Notification | null>(null);
  const namesRef = useRef<Record<string, string>>({});
  const [muted, setMuted] = useState(false);
  const [cam, setCam] = useState(true);
  const [notice, setNotice] = useState("");
  const [ready, setReady] = useState(false);
  const [extraNames, setExtraNames] = useState<Record<string, string>>({});
  const socket = useRef<ReturnType<typeof connectRealtime> | null>(null);
  const client = useMemo(() => api(), []);

  useEffect(() => {
    if (useAuth.persist.hasHydrated()) setReady(true);
    return useAuth.persist.onFinishHydration(() => setReady(true));
  }, []);

  useEffect(() => {
    if (!ready) return;
    if (!session) router.replace("/login/");
  }, [ready, router, session]);

  const me = useQuery({ queryKey: ["me"], enabled: !!session, queryFn: () => client.users.getMe({}) });
  const guilds = useQuery({ queryKey: ["guilds"], enabled: !!session, queryFn: () => client.guilds.listMyGuilds({}) });
  const channels = useQuery({
    queryKey: ["channels", guildId],
    enabled: !!guildId,
    queryFn: () => client.guilds.listChannels({ guildId }),
  });
  const members = useQuery({
    queryKey: ["members", guildId],
    enabled: !!guildId,
    queryFn: async () => {
      const list = await client.guilds.listMembers({ guildId });
      const profiles = await Promise.all(list.members.map((m) => client.users.getProfile({ userId: m.userId }).catch(() => null)));
      return list.members.map((m, i) => ({ ...m, profile: profiles[i] }));
    },
  });
  const dms = useQuery({ queryKey: ["dms"], enabled: !!session, queryFn: () => client.messages.listDMs({}) });
  const friends = useQuery({ queryKey: ["friends"], enabled: !!session, queryFn: () => client.users.listFriends({}) });
  const requests = useQuery({ queryKey: ["requests"], enabled: !!session, queryFn: () => client.users.listIncomingRequests({}) });
  const names = useMemo(() => {
    const out: Record<string, string> = { ...extraNames };
    if (me.data) out[me.data.id] = me.data.displayName || me.data.username;
    for (const rel of friends.data?.relationships ?? []) {
      if (rel.user) out[rel.user.id] = rel.user.displayName || rel.user.username;
    }
    for (const m of members.data ?? []) {
      if (m.profile) out[m.userId] = m.profile.displayName || m.profile.username;
    }
    return out;
  }, [friends.data, members.data, me.data, extraNames]);
  const callerProfile = useQuery({
    queryKey: ["profile", incoming?.fromId],
    enabled: !!incoming && !names[incoming.fromId],
    queryFn: () => client.users.getProfile({ userId: incoming!.fromId }),
  });
  namesRef.current = callerProfile.data
    ? { ...names, [callerProfile.data.id]: callerProfile.data.displayName || callerProfile.data.username }
    : names;
  callRef.current = call;
  incomingRef.current = incoming;
  const nameOf = (id: string) => namesRef.current[id] ?? "Собеседник";
  const activeChannel = guildId ? channelId : dmId;
  const selectedGuildChannel = (channels.data?.channels ?? []).find((ch) => ch.id === channelId);
  const isVoiceChannel = selectedGuildChannel?.type === "voice";
  const channelKind = guildId ? "guild" : dms.data?.channels.find((c) => c.id === dmId)?.kind || "dm";
  const messages = useQuery({
    queryKey: ["messages", activeChannel, channelKind],
    enabled: !!activeChannel && !isVoiceChannel,
    queryFn: () => client.messages.listMessages({ channelId: activeChannel, channelKind, limit: 50 }),
  });

  useEffect(() => {
    if (!session) return;
    const missing = new Set<string>();
    for (const msg of messages.data?.messages ?? []) {
      if (msg.authorId && !namesRef.current[msg.authorId]) missing.add(msg.authorId);
    }
    for (const dm of dms.data?.channels ?? []) {
      for (const id of dm.memberIds) {
        if (id && id !== session.userId && !namesRef.current[id]) missing.add(id);
      }
    }
    for (const id of call?.peers ?? []) {
      if (id && !namesRef.current[id]) missing.add(id);
    }
    const ids = [...missing];
    if (!ids.length) return;
    let cancelled = false;
    void Promise.all(ids.map((userId) => client.users.getProfile({ userId }).catch(() => null))).then((profiles) => {
      if (cancelled) return;
      setExtraNames((prev) => {
        const next = { ...prev };
        let changed = false;
        for (const profile of profiles) {
          if (!profile) continue;
          const label = profile.displayName || profile.username;
          if (next[profile.id] === label) continue;
          next[profile.id] = label;
          changed = true;
        }
        return changed ? next : prev;
      });
    });
    return () => {
      cancelled = true;
    };
  }, [messages.data, dms.data, call?.peers, session, client]);

  useEffect(() => {
    if (!session) return;
    const conn = connectRealtime(session.accessToken, (event) => onRealtime(event));
    socket.current = conn;
    return () => conn.close();
  }, [session?.accessToken]);

  useEffect(() => {
    const guildIds = (guilds.data?.guilds ?? []).map((g) => g.id);
    const dmIds = (dms.data?.channels ?? []).map((c) => c.id);
    socket.current?.send({ type: "subscribe", guild_ids: guildIds, dm_ids: dmIds });
    const ids = [
      ...(friends.data?.relationships ?? []).map((r) => r.user?.id ?? ""),
      ...(members.data ?? []).map((m) => m.userId),
    ].filter(Boolean);
    if (ids.length) socket.current?.send({ type: "presence_query", user_ids: ids });
  }, [guilds.data, dms.data, friends.data, members.data]);

  const dmIds = (dms.data?.channels ?? []).map((c) => c.id).join(",");
  const voiceChannelIds = (channels.data?.channels ?? []).filter((ch) => ch.type === "voice").map((ch) => ch.id).join(",");
  useEffect(() => {
    if (!session) return;
    const rooms = [
      ...dmIds.split(",").filter(Boolean).map((id) => ({ id, kind: "dm" as const })),
      ...voiceChannelIds.split(",").filter(Boolean).map((id) => ({ id, kind: "guild" as const })),
    ];
    if (!rooms.length) return;
    let cancelled = false;
    const base = voiceHttpBase(voiceBase);
    void Promise.all(
      rooms.map(async ({ id, kind }) => {
        const res = await fetch(`${base}/rooms/${kind}/${id}?token=${encodeURIComponent(session.accessToken)}`).catch(() => null);
        if (!res?.ok) return null;
        const body = (await res.json()) as { participants: string[] };
        return [id, body.participants] as const;
      }),
    ).then((rows) => {
      if (cancelled) return;
      setVoiceRooms((prev) => rows.reduce((acc, row) => (row ? applyVoiceState(acc, row[0], row[1]) : acc), prev));
    });
    return () => {
      cancelled = true;
    };
  }, [dmIds, voiceChannelIds, session]);

  useEffect(() => {
    if (!incoming) return;
    const original = document.title;
    let flip = false;
    const blink = window.setInterval(() => {
      flip = !flip;
      document.title = flip ? `Входящий звонок — ${nameOf(incoming.fromId)}` : original;
    }, 1000);
    const expire = window.setTimeout(() => dismissIncoming(), RING_TIMEOUT_MS);
    return () => {
      window.clearInterval(blink);
      window.clearTimeout(expire);
      document.title = original;
    };
  }, [incoming]);

  function dismissIncoming() {
    notificationRef.current?.close();
    notificationRef.current = null;
    setIncoming(null);
  }

  function notifyIncoming(fromId: string) {
    if (typeof Notification === "undefined" || (document.hasFocus() && !document.hidden)) return;
    const show = () => {
      const n = new Notification("Входящий звонок", { body: `${nameOf(fromId)} звонит вам`, tag: "rokko-call", requireInteraction: true });
      n.onclick = () => window.focus();
      notificationRef.current = n;
    };
    if (Notification.permission === "granted") show();
    else if (Notification.permission !== "denied") void Notification.requestPermission().then((p) => p === "granted" && show());
  }

  function onRealtime(event: RealtimeEvent) {
    const me = useAuth.getState().session?.userId ?? "";
    if (event.type === "call_declined" && event.channel_id) {
      const current = callRef.current;
      if (current?.roomId === event.channel_id && !current.connected) {
        endCall();
        setNotice(`${nameOf(event.user_id ?? "")} отклонил(а) звонок`);
      }
      return;
    }
    if (event.type === "event" && event.event_type === "voice.state.updated") {
      const payload = event.payload?.payload;
      if (!payload?.room_id) return;
      const participants = parseParticipants(payload.participants);
      setVoiceRooms((prev) => applyVoiceState(prev, payload.room_id!, participants));
      if (shouldDismissIncoming(incomingRef.current, payload.room_id, participants, me)) dismissIncoming();
      return;
    }
    if (event.type === "typing") {
      setTyping(event.user_id ?? "");
      window.setTimeout(() => setTyping(""), 4000);
      return;
    }
    if (event.type === "presence" && event.user_id) {
      setPresence((prev) => ({ ...prev, [event.user_id!]: event.status === "online" }));
      return;
    }
    if (event.type === "presence_snapshot" && event.online) {
      setPresence((prev) => ({ ...prev, ...event.online }));
      return;
    }
    if (event.type === "event" && event.event_type === "voice.call.missed") {
      const payload = event.payload?.payload;
      if (payload?.room_id && callRef.current?.roomId === payload.room_id && !callRef.current.connected) {
        endCall();
        setNotice("Звонок не отвечен");
      }
      if (incomingRef.current?.roomId === payload?.room_id) dismissIncoming();
      return;
    }
    if (event.type === "event" && event.event_type === "voice.call.ring") {
      const payload = event.payload?.payload;
      if (!payload?.room_id || !payload.from_id || payload.from_id === me) return;
      if (callRef.current?.roomId === payload.room_id) return;
      setIncoming({ roomId: payload.room_id, fromId: payload.from_id });
      notifyIncoming(payload.from_id);
      return;
    }
    if (event.event_type?.startsWith("message.")) {
      void qc.invalidateQueries({ queryKey: ["messages"] });
    }
    if (event.event_type?.startsWith("guild.") || event.event_type?.startsWith("channel.") || event.event_type?.startsWith("member.")) {
      void qc.invalidateQueries({ queryKey: ["guilds"] });
      void qc.invalidateQueries({ queryKey: ["channels"] });
      void qc.invalidateQueries({ queryKey: ["members"] });
    }
  }

  function go(next: Record<string, string>) {
    const q = new URLSearchParams(next);
    router.replace(`/app/?${q.toString()}`);
  }

  const send = useMutation({
    mutationFn: async (file?: File) => {
      const attachments = [];
      if (file) {
        const upload = await client.media.createUploadURL({ filename: file.name, contentType: file.type || "application/octet-stream", size: BigInt(file.size) });
        await fetch(upload.uploadUrl, { method: "PUT", body: file });
        await client.media.confirmUpload({ mediaId: upload.mediaId });
        attachments.push({ mediaId: upload.mediaId, filename: file.name, contentType: file.type, size: BigInt(file.size) });
      }
      if (editing) {
        await client.messages.editMessage({ messageId: editing, content: draft });
        setEditing(null);
      } else {
        await client.messages.sendMessage({ channelId: activeChannel, channelKind, content: draft, attachments });
      }
    },
    onSuccess: () => {
      setDraft("");
      void qc.invalidateQueries({ queryKey: ["messages", activeChannel, channelKind] });
    },
    onError: (err) => setNotice(errorText(err)),
  });

  function endCall() {
    callRef.current?.handle?.hangup();
    callRef.current = null;
    setCall(null);
  }

  async function callRoom(roomId: string, kind: "dm" | "guild", video: boolean) {
    if (!session) return;
    if (callRef.current?.roomId === roomId) return;
    endCall();
    const next: ActiveCall = { roomId, kind, remotes: [], peers: [], connected: false };
    callRef.current = next;
    setCall(next);
    setNotice("");
    const update = (fn: (prev: ActiveCall) => ActiveCall) =>
      setCall((prev) => (prev && prev.roomId === roomId ? fn(prev) : prev));
    try {
      const handle = await startCall(session.accessToken, roomId, kind, video, {
        onLocal: (local) => update((prev) => ({ ...prev, local })),
        onRemote: (stream) => update((prev) => ({ ...prev, remotes: [...prev.remotes.filter((s) => s.id !== stream.id), stream] })),
        onRemoteGone: (streamId) => update((prev) => ({ ...prev, remotes: prev.remotes.filter((s) => s.id !== streamId) })),
        onPeerJoined: (userId) =>
          update((prev) => ({ ...prev, connected: true, peers: prev.peers.includes(userId) ? prev.peers : [...prev.peers, userId] })),
        onPeerLeft: (userId) => update((prev) => ({ ...prev, peers: prev.peers.filter((id) => id !== userId) })),
      });
      if (callRef.current?.roomId !== roomId) {
        handle.hangup();
        return;
      }
      update((prev) => ({ ...prev, handle }));
      setMuted(false);
      setCam(video);
      if (kind === "dm") {
        window.setTimeout(() => {
          const current = callRef.current;
          if (current?.roomId === roomId && !current.connected) {
            endCall();
            setNotice("Нет ответа");
          }
        }, RING_TIMEOUT_MS);
      }
    } catch (err) {
      if (callRef.current?.roomId === roomId) {
        callRef.current = null;
        setCall(null);
      }
      setNotice(err instanceof DOMException && err.name === "NotAllowedError" ? "Нет доступа к микрофону или камере" : "Не удалось начать звонок");
    }
  }

  function acceptIncoming(video: boolean) {
    const current = incomingRef.current;
    if (!current) return;
    dismissIncoming();
    go({ view: "dm", dm: current.roomId });
    void callRoom(current.roomId, "dm", video);
  }

  function declineIncoming() {
    const current = incomingRef.current;
    if (!current) return;
    socket.current?.send({ type: "call_decline", channel_id: current.roomId });
    dismissIncoming();
  }

  if (!ready) return null;
  if (!session) return null;
  const items = (messages.data?.messages ?? []) as Msg[];

  return (
    <div className="flex h-screen bg-bg text-text">
      <aside className="flex w-16 flex-col items-center gap-2 border-r border-line bg-panel py-3">
        <button className="grid h-10 w-10 place-items-center rounded-2xl bg-accent text-white" onClick={() => go({ view: "friends" })} title="Друзья">
          <Users size={18} />
        </button>
        <div className="h-px w-8 bg-line" />
        {(guilds.data?.guilds ?? []).map((g) => (
          <button key={g.id} className="grid h-10 w-10 place-items-center rounded-2xl bg-hover text-sm" onClick={() => go({ view: "guild", guild: g.id })} title={g.name}>
            {g.name.slice(0, 1).toUpperCase()}
          </button>
        ))}
        <button className="grid h-10 w-10 place-items-center rounded-2xl bg-hover" onClick={() => createGuild(client, qc)} title="Создать сервер">
          <Plus size={16} />
        </button>
        <button
          className="grid h-10 w-10 place-items-center rounded-2xl bg-hover"
          onClick={() => void joinGuild(client, qc, (id) => go({ view: "guild", guild: id }), setNotice)}
          title="Войти на сервер по приглашению"
        >
          <LogIn size={16} />
        </button>
      </aside>
      <aside className="flex w-60 flex-col border-r border-line bg-panel">
        <div className="border-b border-line px-3 py-3 text-sm font-semibold">
          {view === "guild" ? guilds.data?.guilds.find((g) => g.id === guildId)?.name : "Личные сообщения"}
        </div>
        <div className="flex-1 space-y-1 overflow-auto p-2">
          {view !== "guild" && (
            <>
              <button className="w-full rounded px-2 py-1 text-left text-sm hover:bg-hover" onClick={() => go({ view: "friends" })}>Друзья</button>
              {(dms.data?.channels ?? []).map((dm) => {
                const inCall = othersInCall(voiceRooms, dm.id, session.userId).length > 0;
                const title = dm.memberIds.filter((id) => id !== session.userId).map(nameOf).join(", ");
                return (
                  <button
                    key={dm.id}
                    className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm hover:bg-hover ${dm.id === dmId ? "bg-hover" : ""}`}
                    onClick={() => go({ view: "dm", dm: dm.id })}
                  >
                    <span className="min-w-0 flex-1 truncate">{dm.kind === "group" ? `Группа: ${title}` : title}</span>
                    {inCall && (
                      <span className="flex shrink-0 items-center gap-1 text-xs text-accent" title="Идёт звонок">
                        <Phone size={12} className="animate-pulse" /> в звонке
                      </span>
                    )}
                  </button>
                );
              })}
            </>
          )}
          {view === "guild" &&
            (channels.data?.channels ?? []).map((ch) => {
              const people = othersInCall(voiceRooms, ch.id, session.userId);
              return (
                <button
                  key={ch.id}
                  className={`w-full rounded px-2 py-1 text-left text-sm hover:bg-hover ${ch.id === channelId ? "bg-hover" : ""}`}
                  onClick={() => {
                    go({ view: "guild", guild: guildId, channel: ch.id });
                    if (ch.type === "voice") void callRoom(ch.id, "guild", false);
                  }}
                >
                  <span className="flex items-center gap-2">
                    {ch.type === "voice" ? <Mic size={14} /> : <Hash size={14} />} {ch.name}
                  </span>
                  {ch.type === "voice" && people.length > 0 && (
                    <span className="mt-1 block truncate pl-5 text-xs text-accent">{people.map(nameOf).join(", ")}</span>
                  )}
                </button>
              );
            })}
        </div>
        <div className="border-t border-line p-3 text-xs text-muted">
          <div>{me.data?.username}#{me.data?.discriminator}</div>
          <button className="mt-1 underline" onClick={() => document.documentElement.classList.toggle("light")}>Тема</button>
          <button className="mt-1 block underline" onClick={() => { clear(); router.replace("/login/"); }}>Выйти</button>
        </div>
      </aside>
      <section className="flex min-w-0 flex-1 flex-col">
        {view === "friends" ? (
          <Friends client={client} friends={friends.data?.relationships ?? []} requests={requests.data?.relationships ?? []} presence={presence} onDm={(id) => go({ view: "dm", dm: id })} />
        ) : (
          <>
            <header className="flex items-center justify-between border-b border-line px-4 py-3">
              <div className="text-sm font-medium">
                {isVoiceChannel
                  ? selectedGuildChannel?.name || "Голосовой канал"
                  : activeChannel
                    ? view === "dm"
                      ? (dms.data?.channels.find((c) => c.id === dmId)?.memberIds ?? [])
                          .filter((id) => id !== session.userId)
                          .map(nameOf)
                          .join(", ") || "Чат"
                      : selectedGuildChannel?.name || "Чат"
                    : "Выберите канал"}
              </div>
              <div className="flex gap-2">
                {view === "guild" && <GuildTools guildId={guildId} client={client} qc={qc} />}
                {view === "dm" && activeChannel && (
                  <>
                    <Button className="bg-hover text-text" onClick={() => void callRoom(activeChannel, "dm", false)}><Phone size={14} /></Button>
                    <Button className="bg-hover text-text" onClick={() => void callRoom(activeChannel, "dm", true)}><Video size={14} /></Button>
                  </>
                )}
                {isVoiceChannel && call?.roomId !== channelId && (
                  <Button onClick={() => void callRoom(channelId, "guild", false)}>Войти</Button>
                )}
              </div>
            </header>
            {view === "dm" && dmId && call?.roomId !== dmId && othersInCall(voiceRooms, dmId, session.userId).length > 0 && (
              <div className="flex items-center justify-between gap-3 border-b border-line bg-accent/10 px-4 py-2 text-sm">
                <span className="flex items-center gap-2 text-accent">
                  <Phone size={14} className="animate-pulse" />
                  {othersInCall(voiceRooms, dmId, session.userId).map(nameOf).join(", ")} в звонке
                </span>
                <div className="flex gap-2">
                  <Button onClick={() => void callRoom(dmId, "dm", false)}>Присоединиться</Button>
                  <Button className="bg-hover text-text" onClick={() => void callRoom(dmId, "dm", true)}><Video size={14} /></Button>
                </div>
              </div>
            )}
            <div className="flex-1 space-y-3 overflow-auto p-4">
              {isVoiceChannel ? (
                <div className="space-y-2 text-sm text-muted">
                  <p>Голосовой канал. Кликни по нему слева, чтобы подключиться.</p>
                  {(voiceRooms[channelId] ?? []).map((id) => (
                    <div key={id} className="text-text">{nameOf(id)}{id === session.userId ? " (вы)" : ""}</div>
                  ))}
                </div>
              ) : (
                items.map((msg) => (
                  <article key={msg.id} className="group">
                    <div className="text-xs text-muted">
                      {nameOf(msg.authorId)} {presence[msg.authorId] ? "· онлайн" : ""} {msg.editedAtUnix > BigInt(0) ? "· изменено" : ""}
                    </div>
                    <p>{msg.content}</p>
                    {msg.attachments?.map((att) => (
                      <button key={att.mediaId} className="text-sm text-accent" onClick={() => download(client, att.mediaId)}>{att.filename}</button>
                    ))}
                    {msg.authorId === session.userId && (
                      <div className="hidden gap-2 text-xs text-muted group-hover:flex">
                        <button onClick={() => { setEditing(msg.id); setDraft(msg.content); }}>Изменить</button>
                        <button onClick={() => client.messages.deleteMessage({ messageId: msg.id }).then(() => qc.invalidateQueries({ queryKey: ["messages"] }))}>Удалить</button>
                      </div>
                    )}
                  </article>
                ))
              )}
              {typing && !isVoiceChannel && <p className="text-xs text-muted">печатает…</p>}
            </div>
            {activeChannel && !isVoiceChannel && (
              <form className="flex gap-2 border-t border-line p-3" onSubmit={(e) => { e.preventDefault(); void send.mutate(undefined); }}>
                <label className="grid cursor-pointer place-items-center rounded-md bg-hover px-3 text-sm">
                  Файл
                  <input className="hidden" type="file" onChange={(e) => { const file = e.target.files?.[0]; if (file) void send.mutate(file); }} />
                </label>
                <Input
                  value={draft}
                  placeholder="Сообщение"
                  onChange={(e) => {
                    setDraft(e.target.value);
                    socket.current?.send({ type: "typing", channel_id: activeChannel, guild_id: guildId });
                  }}
                />
                <Button type="submit">Отправить</Button>
              </form>
            )}
          </>
        )}
        {notice && <p className="px-4 pb-2 text-sm text-red-400">{notice}</p>}
      </section>
      {view === "guild" && (
        <aside className="w-56 border-l border-line bg-panel p-3">
          <div className="mb-2 text-xs uppercase text-muted">Участники</div>
          {(members.data ?? []).map((m) => (
            <div key={m.userId} className="flex items-center justify-between py-1 text-sm">
              <span>{presence[m.userId] ? "● " : "○ "}{m.profile?.displayName || m.profile?.username || m.userId}</span>
              <span className="text-xs text-muted">{m.role}</span>
            </div>
          ))}
        </aside>
      )}
      {incoming && (
        <div className="fixed inset-0 z-30 grid place-items-center bg-black/50" role="dialog" aria-label="Входящий звонок">
          <div className="w-80 rounded-2xl border border-line bg-panel p-6 text-center shadow-2xl">
            <div className="mx-auto grid h-16 w-16 animate-pulse place-items-center rounded-full bg-accent text-2xl font-semibold text-white">
              {nameOf(incoming.fromId).slice(0, 1).toUpperCase()}
            </div>
            <p className="mt-4 text-lg font-semibold">{nameOf(incoming.fromId)}</p>
            <p className="text-sm text-muted">Входящий звонок…</p>
            <div className="mt-6 flex justify-center gap-3">
              <Button onClick={() => acceptIncoming(false)} title="Принять"><Phone size={16} /> Принять</Button>
              <Button className="bg-hover text-text" onClick={() => acceptIncoming(true)} title="Принять с видео"><Video size={16} /></Button>
              <Button className="bg-red-600 text-white hover:bg-red-500" onClick={declineIncoming} title="Отклонить"><PhoneOff size={16} /></Button>
            </div>
          </div>
        </div>
      )}
      {call && (
        <div className="fixed inset-x-0 bottom-0 z-20 border-t border-line bg-panel p-4">
          <p className="mb-3 flex items-center gap-2 text-sm">
            <Phone size={14} className={call.handle ? "text-accent" : "animate-pulse text-muted"} />
            {!call.handle
              ? "Подключение…"
              : call.kind === "guild"
                ? call.peers.length
                  ? `В канале: ${[session.userId, ...call.peers].map(nameOf).join(", ")}`
                  : "Вы в голосовом канале"
                : call.peers.length
                  ? `В звонке: ${call.peers.map(nameOf).join(", ")}`
                  : call.connected
                    ? "Собеседник вышел. Ожидание…"
                    : "Вызов… ожидание ответа"}
          </p>
          <div className="mb-3 flex gap-3 overflow-auto">
            {call.local && call.local.getVideoTracks().length > 0 && (
              <video className="h-28 rounded bg-black" autoPlay muted playsInline ref={(el) => { if (el && el.srcObject !== call.local) el.srcObject = call.local!; }} />
            )}
            {call.remotes.map((stream) => (
              <RemoteMedia key={stream.id} stream={stream} />
            ))}
            {call.peers.map((id) => (
              <div key={id} className="grid h-28 w-28 place-items-center rounded bg-hover text-sm">{nameOf(id)}</div>
            ))}
          </div>
          <div className="flex gap-2">
            <Button className="bg-hover text-text" disabled={!call.handle} onClick={() => { const next = !muted; setMuted(next); call.handle?.mute(next); }}>{muted ? "Включить микрофон" : "Выключить микрофон"}</Button>
            {call.local && call.local.getVideoTracks().length > 0 && (
              <Button className="bg-hover text-text" onClick={() => { const next = !cam; setCam(next); call.handle?.camera(next); }}>{cam ? "Выключить камеру" : "Включить камеру"}</Button>
            )}
            <Button className="bg-red-600 text-white hover:bg-red-500" onClick={endCall}>Завершить</Button>
          </div>
        </div>
      )}
      <span className="hidden">{wsBase}</span>
    </div>
  );
}

function RemoteMedia({ stream }: { stream: MediaStream }) {
  const hasVideo = stream.getVideoTracks().some((track) => track.readyState !== "ended");
  return (
    <>
      {hasVideo ? (
        <video
          className="h-28 rounded bg-black"
          autoPlay
          playsInline
          ref={(el) => {
            if (!el) return;
            if (el.srcObject !== stream) el.srcObject = stream;
            void el.play().catch(() => undefined);
          }}
        />
      ) : (
        <audio
          autoPlay
          ref={(el) => {
            if (!el) return;
            if (el.srcObject !== stream) el.srcObject = stream;
            void el.play().catch(() => undefined);
          }}
        />
      )}
    </>
  );
}

function Friends({
  client,
  friends,
  requests,
  presence,
  onDm,
}: {
  client: ReturnType<typeof api>;
  friends: { user?: { id: string; username: string; discriminator: string } | undefined }[];
  requests: { user?: { id: string; username: string } | undefined }[];
  presence: Record<string, boolean>;
  onDm: (id: string) => void;
}) {
  const qc = useQueryClient();
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<{ id: string; username: string; discriminator: string }[]>([]);
  return (
    <div className="space-y-4 p-6">
      <h2 className="text-lg font-semibold">Друзья</h2>
      <form className="flex gap-2" onSubmit={async (e) => { e.preventDefault(); const res = await client.users.searchUsers({ query, limit: 10 }); setFound(res.users); }}>
        <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Найти username или username#0000" />
        <Button type="submit">Найти</Button>
      </form>
      {found.map((user) => (
        <div key={user.id} className="flex items-center justify-between text-sm">
          <span>{user.username}#{user.discriminator}</span>
          <Button onClick={() => client.users.sendFriendRequest({ userId: user.id }).then(() => qc.invalidateQueries({ queryKey: ["requests"] }))}>Добавить</Button>
        </div>
      ))}
      <h3 className="text-sm text-muted">Входящие заявки</h3>
      {requests.map((rel) => rel.user && (
        <div key={rel.user.id} className="flex gap-2 text-sm">
          <span>{rel.user.username}</span>
          <button onClick={() => client.users.acceptFriendRequest({ userId: rel.user!.id }).then(() => qc.invalidateQueries())}>Принять</button>
          <button onClick={() => client.users.declineFriendRequest({ userId: rel.user!.id })}>Отклонить</button>
        </div>
      ))}
      <h3 className="text-sm text-muted">Список</h3>
      {friends.map((rel) => rel.user && (
        <div key={rel.user.id} className="flex items-center justify-between text-sm">
          <span>{presence[rel.user.id] ? "●" : "○"} {rel.user.username}#{rel.user.discriminator}</span>
          <Button className="bg-hover text-text" onClick={() => client.messages.openDM({ userId: rel.user!.id }).then((dm) => onDm(dm.id))}>Написать</Button>
          <Button className="bg-hover text-text" onClick={() => {
            const extra = window.prompt("ID участников через запятую, включая этого друга");
            if (!extra) return;
            const userIds = extra.split(",").map((v) => v.trim()).filter(Boolean);
            void client.messages.createGroupDM({ userIds }).then((dm) => onDm(dm.id));
          }}>Группа</Button>
        </div>
      ))}
    </div>
  );
}

function GuildTools({ guildId, client, qc }: { guildId: string; client: ReturnType<typeof api>; qc: ReturnType<typeof useQueryClient> }) {
  return (
    <div className="flex gap-2">
      <Button className="bg-hover text-text" onClick={() => renameGuild(client, qc, guildId)}>Сервер</Button>
      <Button className="bg-hover text-text" onClick={() => createChannel(client, qc, guildId)}>Канал</Button>
      <Button className="bg-hover text-text" onClick={() => invite(client, guildId)}>Инвайт</Button>
    </div>
  );
}

async function createGuild(client: ReturnType<typeof api>, qc: ReturnType<typeof useQueryClient>) {
  const name = window.prompt("Название сервера");
  if (!name) return;
  await client.guilds.createGuild({ name, iconUrl: "" });
  await qc.invalidateQueries({ queryKey: ["guilds"] });
}

async function joinGuild(
  client: ReturnType<typeof api>,
  qc: ReturnType<typeof useQueryClient>,
  openGuild: (guildId: string) => void,
  onError: (message: string) => void,
) {
  const code = window.prompt("Код приглашения на сервер");
  if (!code?.trim()) return;
  try {
    const guild = await client.guilds.joinByInvite({ code: code.trim() });
    await qc.invalidateQueries({ queryKey: ["guilds"] });
    openGuild(guild.id);
  } catch (err) {
    onError(errorText(err));
  }
}

async function renameGuild(client: ReturnType<typeof api>, qc: ReturnType<typeof useQueryClient>, guildId: string) {
  const name = window.prompt("Новое название");
  if (!name) return;
  await client.guilds.updateGuild({ guildId, name, iconUrl: "" });
  await qc.invalidateQueries({ queryKey: ["guilds"] });
}

async function createChannel(client: ReturnType<typeof api>, qc: ReturnType<typeof useQueryClient>, guildId: string) {
  const name = window.prompt("Название канала");
  if (!name) return;
  const type = window.confirm("Голосовой канал? Отмена — текстовый") ? "voice" : "text";
  await client.guilds.createChannel({ guildId, name, type });
  await qc.invalidateQueries({ queryKey: ["channels", guildId] });
}

async function invite(client: ReturnType<typeof api>, guildId: string) {
  const inv = await client.guilds.createInvite({ guildId, maxUses: 0, expiresInSeconds: BigInt(86400) });
  window.prompt("Код приглашения", inv.code);
}

async function download(client: ReturnType<typeof api>, mediaId: string) {
  const res = await client.media.getDownloadURL({ mediaId });
  window.open(res.url, "_blank");
}
