'use strict';

const tg = window.Telegram && window.Telegram.WebApp;
if (tg) { tg.ready(); tg.expand(); }

const API = 'api';
const state = { movies: [], tab: 'want', query: '', current: null };

const $ = (id) => document.getElementById(id);
const grid = $('grid');

function authHeaders() {
  return { 'X-Telegram-Init-Data': tg ? tg.initData : '' };
}

// для <img>/<video> заголовок не выставить — авторизация через query
function mediaURL(name) {
  return `${API}/media/${name}?initData=${encodeURIComponent(tg ? tg.initData : '')}`;
}

async function api(path, opts = {}) {
  const res = await fetch(`${API}/${path}`, {
    ...opts,
    headers: { ...authHeaders(), ...(opts.headers || {}) },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `http ${res.status}`);
  return body;
}

function toast(text) {
  const el = document.createElement('div');
  el.className = 'toast';
  el.textContent = text;
  document.body.appendChild(el);
  setTimeout(() => el.remove(), 1800);
}

const kindRu = { movie: 'фильм', 'tv-series': 'сериал', cartoon: 'мультфильм', anime: 'аниме', 'animated-series': 'мультсериал' };

function visible() {
  const q = state.query.toLowerCase();
  return state.movies.filter((m) =>
    m.status === state.tab && (!q || m.title.toLowerCase().includes(q)));
}

function render() {
  const list = visible();
  grid.replaceChildren(...list.map(card));
  $('empty').hidden = list.length > 0;
  const want = state.movies.filter((m) => m.status === 'want').length;
  $('count').textContent = state.movies.length ? `${want} в очереди` : '';
}

function card(m) {
  const btn = document.createElement('button');
  btn.className = 'card';
  btn.addEventListener('click', () => openSheet(m));

  const wrap = document.createElement('div');
  wrap.className = 'poster-wrap';
  const posterSrc = m.poster_url || (m.reel_thumb ? mediaURL(m.reel_thumb) : '');
  if (posterSrc) {
    const img = document.createElement('img');
    img.src = posterSrc;
    img.alt = '';
    img.loading = 'lazy';
    img.addEventListener('error', () => { img.remove(); wrap.append(fallback(m.title)); });
    wrap.append(img);
  } else {
    wrap.append(fallback(m.title));
  }
  if (m.reel_video || m.reel_thumb) {
    const reelMark = document.createElement('span');
    reelMark.className = 'reel-mark';
    reelMark.textContent = '🎞';
    wrap.append(reelMark);
  }
  if (m.rating_kp > 0) wrap.append(badge('badge-kp', m.rating_kp.toFixed(1)));
  if (m.rating_imdb > 0) wrap.append(badge('badge-imdb', m.rating_imdb.toFixed(1)));
  if (m.status === 'watched') {
    const mark = document.createElement('span');
    mark.className = 'watched-mark';
    mark.textContent = '✓';
    wrap.append(mark);
  }

  const caption = document.createElement('div');
  caption.className = 'card-caption';
  const title = document.createElement('div');
  title.className = 'card-title';
  title.textContent = m.title;
  const sub = document.createElement('div');
  sub.className = 'card-sub';
  sub.textContent = [m.year || '', kindRu[m.kind] || m.kind].filter(Boolean).join(' · ');
  caption.append(title, sub);

  btn.append(wrap, caption);
  return btn;
}

function fallback(title) {
  const el = document.createElement('div');
  el.className = 'poster-fallback';
  el.textContent = title;
  return el;
}

function badge(cls, text) {
  const el = document.createElement('span');
  el.className = cls;
  el.textContent = text;
  return el;
}

/* --- bottom sheet --- */

function openSheet(m) {
  state.current = m;
  $('sheet-poster').src = m.poster_url || '';
  $('sheet-poster').hidden = !m.poster_url;
  $('sheet-title').textContent = m.title;
  $('sheet-meta').textContent = [m.year || '', kindRu[m.kind] || m.kind].filter(Boolean).join(' · ');
  const r = $('sheet-ratings');
  r.replaceChildren();
  if (m.rating_kp > 0) r.append(span('rk', `КП ${m.rating_kp.toFixed(1)}`));
  if (m.rating_imdb > 0) r.append(span('ri', `IMDb ${m.rating_imdb.toFixed(1)}`));
  $('sheet-comment').textContent = m.comment || '';
  $('sheet-comment').hidden = !m.comment;
  renderReel(m);

  const reel = $('link-reel');
  reel.hidden = !m.reel_url;
  if (m.reel_url) reel.href = m.reel_url;
  const film = $('link-film');
  const filmURL = m.kp_url || m.imdb_url;
  film.hidden = !filmURL;
  if (filmURL) {
    film.href = filmURL;
    film.textContent = m.kp_url ? 'Кинопоиск' : 'IMDb';
  }

  $('btn-toggle').textContent = m.status === 'want' ? '✓ Посмотрел' : '↩ Вернуть в очередь';
  $('sheet').hidden = false;
  $('sheet-backdrop').hidden = false;
}

function span(cls, text) {
  const el = document.createElement('span');
  el.className = cls;
  el.textContent = text;
  return el;
}

// превью скачанного рилса; тап — локальный плеер
function renderReel(m) {
  const box = $('sheet-reel');
  box.replaceChildren();
  box.hidden = !m.reel_video;
  if (!m.reel_video) return;

  const thumb = document.createElement('div');
  thumb.className = 'reel-preview';
  if (m.reel_thumb) {
    const img = document.createElement('img');
    img.src = mediaURL(m.reel_thumb);
    img.alt = '';
    thumb.append(img);
  }
  const play = document.createElement('span');
  play.className = 'reel-play';
  play.textContent = '▶';
  thumb.append(play);
  thumb.addEventListener('click', () => {
    const video = document.createElement('video');
    video.src = mediaURL(m.reel_video);
    video.controls = true;
    video.autoplay = true;
    video.playsInline = true;
    box.replaceChildren(video);
  });
  box.append(thumb);
}

function closeSheet() {
  state.current = null;
  $('sheet-reel').replaceChildren(); // остановить видео
  $('sheet').hidden = true;
  $('sheet-backdrop').hidden = true;
}

async function toggleStatus() {
  const m = state.current;
  if (!m) return;
  const next = m.status === 'want' ? 'watched' : 'want';
  try {
    await api(`movie/${m.id}`, { method: 'PATCH', body: JSON.stringify({ status: next }) });
    m.status = next;
    if (tg && tg.HapticFeedback) tg.HapticFeedback.notificationOccurred('success');
    closeSheet();
    render();
  } catch (e) {
    toast(`Не вышло: ${e.message}`);
  }
}

function confirmDialog(text, cb) {
  if (tg && tg.showConfirm) tg.showConfirm(text, (ok) => ok && cb());
  else if (window.confirm(text)) cb();
}

function deleteMovie() {
  const m = state.current;
  if (!m) return;
  confirmDialog(`Удалить «${m.title}»?`, async () => {
    try {
      await api(`movie/${m.id}`, { method: 'DELETE' });
      state.movies = state.movies.filter((x) => x.id !== m.id);
      closeSheet();
      render();
      toast('Удалено');
    } catch (e) {
      toast(`Не вышло: ${e.message}`);
    }
  });
}

/* --- события --- */

document.querySelectorAll('.tab').forEach((btn) =>
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach((b) => b.classList.remove('active'));
    btn.classList.add('active');
    state.tab = btn.dataset.tab;
    render();
  }));

$('search').addEventListener('input', (e) => {
  state.query = e.target.value.trim();
  render();
});

$('sheet-backdrop').addEventListener('click', closeSheet);
$('btn-toggle').addEventListener('click', toggleStatus);
$('btn-delete').addEventListener('click', deleteMovie);
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeSheet(); });

async function load() {
  try {
    const data = await api('list');
    state.movies = data.movies;
    render();
  } catch (e) {
    $('empty').hidden = false;
    $('empty').querySelector('p:last-child').textContent =
      tg && tg.initData ? `Ошибка загрузки: ${e.message}` : 'Открой через Telegram — снаружи список недоступен.';
  }
}

load();
