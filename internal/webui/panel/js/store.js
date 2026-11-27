/* One copy of the live state, polled once and shared.
 *
 * Every screen reads the same tunnel list. Without this each one would poll
 * for itself, so opening a dialog would double the request rate and the card
 * behind it could show a different state from the dialog in front of it.
 *
 * Tunnel cards refresh every second while visible. Host and alert readings
 * keep their own cadence.
 */

import * as api from './api.js';

const state = { stats: null, tunnels: [], alerts: null, error: null };
const subs = new Set();
let timers = [];
let tunnelRequest;

export function get() { return state; }

export function subscribe(fn) {
  subs.add(fn);
  if (state.stats) fn(state);
  return () => subs.delete(fn);
}

function emit() { subs.forEach(fn => fn(state)); }

export async function loadStats() {
  try { state.stats = await api.stats(); state.error = null; }
  catch (e) { state.error = e; }
  emit();
  return state.stats;
}

export function loadTunnels() {
  if (tunnelRequest) return tunnelRequest;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 4000);
  tunnelRequest = api.tunnels(controller.signal)
    .then(rows => { state.tunnels = rows || []; state.error = null; })
    .catch(error => { state.error = error; })
    .then(() => { emit(); return state.tunnels; })
    .finally(() => {
      clearTimeout(timeout);
      tunnelRequest = null;
    });
  return tunnelRequest;
}

export async function loadAlerts() {
  try { state.alerts = await api.alerts(); } catch (e) { /* the badge is not worth an error */ }
  emit();
  return state.alerts;
}

export function tunnel(name) { return state.tunnels.find(t => t.name === name) || null; }

/* Background tabs resume polling as soon as they become visible. */
export function startPolling() {
  stopPolling();
  timers = [
    setInterval(() => { if (!document.hidden) loadStats(); }, 4000),
    setInterval(() => { if (!document.hidden) loadTunnels(); }, 1000),
    setInterval(() => { if (!document.hidden) loadAlerts(); }, 30000),
  ];
  document.addEventListener('visibilitychange', onVisible);
}

function onVisible() { if (!document.hidden) { loadStats(); loadTunnels(); } }

function stopPolling() {
  timers.forEach(clearInterval);
  timers = [];
  document.removeEventListener('visibilitychange', onVisible);
}

/* After an action the card is stale until the next poll, so ask immediately. */
export async function refresh() { await Promise.all([loadStats(), loadTunnels()]); }
