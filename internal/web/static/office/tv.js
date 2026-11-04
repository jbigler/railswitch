// The lounge's TV (D168): a source from /office/tv.json plays on the big
// screen. Jellyfin: the playlist is looked up by name at each start, so an
// edit made in Jellyfin shows the next time; its videos play in turn, round
// and round, through a <video> the screen draws from. One plays at a time.
import { THREE } from './world.js';

const video = document.createElement('video');
video.crossOrigin = 'anonymous'; // WebGL draws only frames CORS lets it read
video.playsInline = true;
let on = null; // {mesh, map, tex, src, items, i, fails, say}

// tvPlaying is the source playing on mesh, or null.
export const tvPlaying = (mesh) => (on && on.mesh === mesh ? on.src : null);

// tvStop stops the TV and gives the screen its own picture back.
export function tvStop() {
  if (!on) return;
  video.pause();
  video.removeAttribute('src');
  video.load();
  on.mesh.material.map = on.map;
  on.mesh.material.needsUpdate = true;
  on.tex.dispose();
  on = null;
}

// tvPlay plays a Jellyfin source ({url, playlist, user, key}) on mesh;
// say(text) reports what goes wrong.
export async function tvPlay(mesh, src, say) {
  tvStop();
  video.muted = false;
  let items;
  try {
    items = await jellyfinPlaylist(src);
  } catch (e) {
    say(`Jellyfin: ${e.message}`);
    return;
  }
  const tex = new THREE.VideoTexture(video);
  tex.colorSpace = THREE.SRGBColorSpace;
  on = { mesh, map: mesh.material.map, tex, src, items, i: 0, fails: 0, say };
  mesh.material.map = tex;
  mesh.material.needsUpdate = true;
  play();
}

function play() {
  video.src = jellyfinStream(on.src, on.items[on.i].Id);
  video.play().catch((e) => {
    // the click's user activation ran out during the lookups: play muted
    // rather than not at all; picking the source again brings the sound
    if (e.name === 'NotAllowedError' && on && !video.muted) {
      video.muted = true;
      on.say('Playing muted: the browser held back the sound. Pick it again for sound.');
      play();
    }
  }); // a failed load arrives as 'error' below
}

function next() {
  on.i = (on.i + 1) % on.items.length;
  play();
}

video.addEventListener('playing', () => { if (on) on.fails = 0; });
video.addEventListener('ended', () => { if (on) next(); });
video.addEventListener('error', () => {
  if (!on || !video.getAttribute('src')) return;
  if (++on.fails >= on.items.length) { // none of them plays: stop, don't spin
    const say = on.say;
    tvStop();
    say('Jellyfin: none of the playlist would play');
    return;
  }
  next();
});

// jellyfinQuery is a Jellyfin URL with the key in its query: a <video>
// can't send a header, and a query keeps fetch() a simple request, with no
// CORS preflight. ApiKey is the name since Jellyfin 10.11, api_key before.
function jellyfinQuery(src, path, params = {}) {
  const q = new URLSearchParams(params);
  q.set('ApiKey', src.key);
  q.set('api_key', src.key);
  return `${src.url}${path}?${q}`;
}

async function jellyfin(src, path, params) {
  const res = await fetch(jellyfinQuery(src, path, params), { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`);
  return res.json();
}

// jellyfinPlaylist is the playlist's videos, found by its exact name; with
// a user, that user's playlists.
async function jellyfinPlaylist(src) {
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
  const got = await jellyfin(src, `/Playlists/${encodeURIComponent(pl.Id)}/Items`, u);
  const items = got.Items.filter((x) => x.MediaType === 'Video');
  if (!items.length) throw new Error(`"${src.playlist}" has no videos`);
  return items;
}

// jellyfinStream asks for H.264 and AAC in MP4, which every browser plays;
// Jellyfin copies what already is and transcodes the rest.
const jellyfinStream = (src, id) => jellyfinQuery(src, `/Videos/${encodeURIComponent(id)}/stream.mp4`, {
  container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', maxWidth: '1920',
});
