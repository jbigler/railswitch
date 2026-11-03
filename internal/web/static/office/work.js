// Whose work the office shows: yours (issues assigned to you; PRs you
// wrote, are assigned or are asked to review), the people you follow, or
// everyone. The follow list and the choice are this browser's (per viewer,
// as a convenience: nothing else reads them).
const KEY = 'railswitch.cyberpunk.work';

const LOGIN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/;

function load() {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || '{}');
    return {
      who: ['mine', 'following', 'all'].includes(v.who) ? v.who : 'mine',
      follow: Array.isArray(v.follow) ? v.follow.filter((l) => typeof l === 'string' && LOGIN.test(l)) : [],
    };
  } catch {
    return { who: 'mine', follow: [] };
  }
}

export const work = load();

function save() {
  try { localStorage.setItem(KEY, JSON.stringify(work)); } catch { /* private window: this visit only */ }
}

export function setWho(who) {
  work.who = who;
  save();
}

// follow adds a login; false when it isn't one.
export function follow(login) {
  login = login.trim().replace(/^@/, '');
  if (!LOGIN.test(login)) return false;
  if (!work.follow.some((l) => l.toLowerCase() === login.toLowerCase())) work.follow.push(login);
  save();
  return true;
}

export function unfollow(login) {
  work.follow = work.follow.filter((l) => l !== login);
  save();
}

// people are the logins the current choice shows, null for everyone.
function people(me) {
  if (work.who === 'all') return null;
  const p = work.who === 'mine' ? [me] : work.follow;
  return new Set(p.filter(Boolean).map((l) => l.toLowerCase()));
}

const has = (set, logins) => logins.some((l) => set.has(String(l).toLowerCase()));

// issuesFor are the issues the current choice shows: assigned to the people.
export function issuesFor(doc) {
  const p = people(doc.me);
  return p ? doc.issues.filter((i) => has(p, i.assignees)) : doc.issues;
}

// prsFor are the PRs the current choice shows: written by, assigned to or
// awaiting a review from the people.
export function prsFor(doc) {
  const p = people(doc.me);
  return p ? doc.prs.filter((x) => has(p, [x.author, ...x.assignees, ...x.reviewers])) : doc.prs;
}

// whoLabel names the current choice for a screen's title.
export function whoLabel(me) {
  if (work.who === 'all') return 'everyone';
  if (work.who === 'mine') return me ? `@${me}` : 'you';
  return work.follow.length ? `following ${work.follow.length}` : 'following nobody';
}
