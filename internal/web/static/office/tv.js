// The lounge's TV (D168): a source from /office/tv.json plays on the big
// screen. Jellyfin: the playlist is looked up by name each time its list
// opens, so an edit made in Jellyfin shows; its videos play in turn, round
// and round, through one <video> the screen draws from. The TV is the
// page's, not a floor's: the elevator takes it along to the next floor's
// lounge (tvAttach), the same video on, and its sound falls off with the
// walker's distance from the screen. With a Jellyfin user, where a video
// stopped goes back to Jellyfin, and it resumes there.
import { THREE } from './world.js';

const video = document.createElement('video');
video.crossOrigin = 'anonymous'; // WebGL draws only frames CORS lets it read
video.playsInline = true;
const tex = new THREE.VideoTexture(video);
tex.colorSpace = THREE.SRGBColorSpace;
const REF = 5; // m: full volume out to the sofa (4.7 m), quieter beyond

let screen = null; // the room's big screen (a group; .screen is its mesh)
let own = null; // the screen mesh's own picture while the TV covers it
let ear = null; // the camera
// on is what plays: {src, pl: {items, userId}, i, offset, abs, fails, say,
// pos: {id: s}, done: Set of ids}; null when off.
let on = null;

// tvEar sets what the sound's distance is measured from.
export function tvEar(camera) { ear = camera; }

// tvAttach moves the TV to a room's big screen (null: none); call it
// before the old room is freed, so its picture comes back, not the video.
export function tvAttach(g) {
  cover(false);
  screen = g;
  cover(true);
}

function cover(yes) {
  const m = screen?.screen;
  if (!m || !on || yes === (own !== null)) return;
  if (yes) {
    own = m.material.map;
    m.material.map = tex;
  } else {
    m.material.map = own;
    own = null;
  }
  m.material.needsUpdate = true;
}

// tvState is what plays, for the panel: {src, items, i, paused, muted,
// pos, dur} in seconds, or null.
export function tvState() {
  if (!on) return null;
  const it = on.pl.items[on.i];
  return { src: on.src, items: on.pl.items, i: on.i, paused: video.paused, muted: video.muted, pos: pos(), dur: (it.RunTimeTicks || 0) / 1e7 };
}

// tvPlay plays item i of pl (from tvPlaylist) from source src, where it
// stopped last; say(text) reports what goes wrong.
export function tvPlay(src, pl, i, say) {
  report();
  const keep = on;
  on = { src, pl, i, say, fails: 0, pos: keep?.pos ?? {}, done: keep?.done ?? new Set(), was: keep && { src: keep.src, session: keep.session } };
  if (!keep) video.muted = false;
  cover(true);
  start(resumeAt(pl.items[i]));
}

export function tvPause() {
  if (!on) return;
  if (video.paused) video.play().catch(() => {});
  else {
    video.pause();
    report();
  }
}

// tvSkip plays the next (+1) or previous (-1) video, round the playlist.
export function tvSkip(d) {
  if (!on) return;
  report();
  skip(d);
}

function skip(d) {
  const n = on.pl.items.length;
  on.i = (on.i + d + n) % n;
  start(resumeAt(on.pl.items[on.i]));
}

export function tvMute() { video.muted = !video.muted; }

// tvSeek restarts the stream at s seconds: a transcoded stream can't seek
// by range, so Jellyfin starts it there (StartTimeTicks).
export function tvSeek(s) {
  if (on) start(Math.max(0, s));
}

// tvStop stops the TV and gives the screen its own picture back.
export function tvStop() {
  if (!on) return;
  report();
  cover(false);
  const was = on;
  on = null;
  video.pause();
  video.removeAttribute('src');
  video.load();
  kill(was.src, was.session);
}

function resumeAt(it) {
  if (on.done.has(it.Id)) return 0;
  if (it.Id in on.pos) return on.pos[it.Id];
  const u = it.UserData;
  return u && !u.Played ? (u.PlaybackPositionTicks || 0) / 1e7 : 0;
}

const unique = () => Date.now().toString(36) + Math.random().toString(36).slice(2);
// The video codecs this browser plays in MP4: one of them is copied, not
// transcoded, which a small Jellyfin server can't do much of.
const CODECS = ['h264', ...Object.entries({ hevc: 'hvc1.2.4.L120.90', av1: 'av01.0.08M.10', vp9: 'vp09.02.40.10' })
  .filter(([, c]) => video.canPlayType(`video/mp4; codecs="${c}"`)).map(([k]) => k)].join(',');
const DEVICE = `railswitch-${unique()}`; // this page, to Jellyfin

function start(at) {
  const was = on.was ?? { src: on.src, session: on.session };
  on.was = null;
  on.offset = at;
  on.abs = null;
  // A fresh PlaySessionId each start: Jellyfin names the transcode's file
  // without StartTimeTicks, and serves an existing one from its beginning.
  on.session = unique();
  // A bitrate, or Jellyfin scales a transcode down to 416 px wide; stereo
  // audio is cheaper to make than 5.1.
  const p = {
    container: 'mp4', videoCodec: CODECS, audioCodec: 'aac', maxWidth: '1920', VideoBitrate: '20000000',
    AudioBitrate: '192000', MaxAudioChannels: '2', DeviceId: DEVICE, PlaySessionId: on.session,
  };
  if (at > 0) p.StartTimeTicks = String(Math.round(at * 1e7));
  video.src = jellyfinURL(on.src, `/Videos/${encodeURIComponent(on.pl.items[on.i].Id)}/stream.mp4`, p);
  kill(was.src, was.session); // after the new src, or the cut stream would end and skip
  video.play().catch((e) => {
    // the click's user activation ran out during the lookups: play muted
    // rather than not at all; the mute button brings the sound
    if (e.name === 'NotAllowedError' && on && !video.muted) {
      video.muted = true;
      on.say('Playing muted: the browser held back the sound. Unmute it in the list.');
      video.play().catch(() => {});
    }
  }); // a failed load arrives as 'error' below
}

// pos is where the video is, in seconds of the whole video: a stream
// started at offset counts from 0, unless Jellyfin kept the timestamps.
function pos() {
  return on.abs ? video.currentTime : on.offset + video.currentTime;
}

// report tells Jellyfin where the video is, or that it was watched to the
// end; only for a user, and never in the way of playing.
function report(played = false) {
  if (!on || on.offset === undefined) return;
  const id = on.pl.items[on.i].Id;
  if (played) on.done.add(id);
  else {
    on.done.delete(id);
    on.pos[id] = pos();
  }
  if (!on.pl.userId) return;
  const body = played ? { Played: true, PlaybackPositionTicks: 0 } : { PlaybackPositionTicks: Math.round(pos() * 1e7) };
  fetch(jellyfinURL(on.src, `/UserItems/${encodeURIComponent(id)}/UserData`, { userId: on.pl.userId }), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), keepalive: true,
  }).catch(() => {});
}

video.addEventListener('playing', () => {
  if (!on) return;
  on.fails = 0;
  if (on.abs === null) on.abs = on.offset > 2 && video.currentTime > on.offset - 2;
});
video.addEventListener('ended', () => {
  if (!on) return;
  report(true);
  skip(1);
});
video.addEventListener('error', () => {
  if (!on || !video.getAttribute('src')) return;
  if (++on.fails >= on.pl.items.length) { // none of them plays: stop, don't spin
    const say = on.say;
    tvStop();
    say('Jellyfin: none of the playlist would play');
    return;
  }
  skip(1);
});
setInterval(() => {
  if (on && !video.paused) report();
}, 15000);
addEventListener('pagehide', () => {
  report();
  if (on) kill(on.src, on.session);
});

// kill stops a session's transcode now: Jellyfin would let it run on for
// 10 s or more after its stream is dropped, at full speed.
function kill(src, session) {
  if (!session) return;
  fetch(jellyfinURL(src, '/Videos/ActiveEncodings', { deviceId: DEVICE, playSessionId: session }), { method: 'DELETE', keepalive: true }).catch(() => {});
}

// The sound falls off past the sofa, (REF/d)^2 beyond REF, and the room's
// walls (the screen group's userData.room: {minX, maxX, minZ, maxZ}) take
// it down to WALLS over the first FADE m outside them: a step out of the
// lounge must be heard, and distance alone is only a few dB.
const WALLS = 0.2;
const FADE = 1.5;
function loudness(d, outside) {
  return Math.min(1, (REF / Math.max(d, REF)) ** 2) * (WALLS + (1 - WALLS) * Math.max(0, 1 - outside / FADE));
}
const at = new THREE.Vector3();
const me = new THREE.Vector3();
setInterval(() => {
  if (!on || !ear || !screen) return;
  ear.getWorldPosition(me);
  const r = screen.userData.room;
  const outside = r ? Math.hypot(Math.max(r.minX - me.x, 0, me.x - r.maxX), Math.max(r.minZ - me.z, 0, me.z - r.maxZ)) : 0;
  video.volume = loudness(screen.getWorldPosition(at).distanceTo(me), outside);
}, 100);

// jellyfinURL is a Jellyfin URL with the key in its query: a <video> can't
// send a header, and a query keeps a GET a simple request, with no CORS
// preflight. ApiKey is the name since Jellyfin 10.11, api_key before.
function jellyfinURL(src, path, params = {}) {
  const q = new URLSearchParams(params);
  q.set('ApiKey', src.key);
  q.set('api_key', src.key);
  return `${src.url}${path}?${q}`;
}

async function jellyfin(src, path, params) {
  const res = await fetch(jellyfinURL(src, path, params), { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`);
  return res.json();
}

// tvPlaylist is the playlist's videos, found by its exact name, with the
// user's id when there is one (their playlists, their resume points):
// {items: [{Id, Name, RunTimeTicks, UserData}], userId}.
export async function tvPlaylist(src) {
  if (!src.key) throw new Error('no API key: railswitch office tv jellyfin key');
  const u = {};
  if (src.user) {
    const users = await jellyfin(src, '/Users');
    u.userId = users.find((x) => x.Name === src.user)?.Id;
    if (!u.userId) throw new Error(`no user "${src.user}"`);
  }
  const found = await jellyfin(src, '/Items', { ...u, IncludeItemTypes: 'Playlist', Recursive: 'true', SearchTerm: src.playlist });
  const pl = found.Items.find((x) => x.Name === src.playlist);
  if (!pl) throw new Error(`no playlist "${src.playlist}"`);
  const got = await jellyfin(src, `/Playlists/${encodeURIComponent(pl.Id)}/Items`, { ...u, EnableUserData: 'true' });
  const items = got.Items.filter((x) => x.MediaType === 'Video');
  if (!items.length) throw new Error(`"${src.playlist}" has no videos`);
  return { items, userId: u.userId };
}
