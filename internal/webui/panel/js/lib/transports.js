import { esc } from './dom.js';

const sources = new Map([
  ['anytls', 'Backhaul'],
  ['native-xtcpmux', 'Backhaul'],
  ['native-xwsmux', 'Backhaul'],
  ['native-tun-tcp', 'Backhaul'],
  ['gretap', 'Gretap'],
  ['l2tpv3', 'VortexL2'],
  ['easytier', 'VortexL2'],
]);

const gostNames = new Map([
  ['gost', 'GOST 3'],
  ['gost-direct', 'GOST 3'],
  ['gost-direct-v2', 'GOST 2'],
]);

export function transportDisplay(id, label) {
  const gostName = gostNames.get(id);
  if (gostName) return { name: gostName, source: '' };
  // Use the original transport name even for results saved by an older panel.
  const dagger = id?.replace(/^native-/, '').match(/^dagger-(http|https|xhttp|xhttps|quantum-gaming|quantum\+?|nova|dc6|tun(?:-(tcp|udp|icmp|gre|ipip|bip))?)$/);
  if (dagger) {
    const names = { 'quantum-gaming': 'quantum / gaming' };
    return { name: dagger[2] ? 'tun / ' + dagger[2] : names[dagger[1]] || dagger[1], source: 'Dagger' };
  }
  if (id?.startsWith('native-dagger-')) return { name: label, source: 'Dagger' };
  if (id?.startsWith('native-')) return { name: label, source: sources.get(id) || 'BackPack' };
  if (id?.startsWith('backhaul-')) return { name: label, source: 'Backhaul' };
  if (id?.startsWith('dagger-')) {
    return { name: label.replace(/^((?:Reverse|Direct) )?Dagger /, '$1'), source: 'Dagger' };
  }
  return { name: label, source: id?.startsWith('omni-') ? 'OmniTunnel' : sources.get(id) };
}

export function transportKey(label) {
  return label ? '<span class="key2 transport-key">' + esc(label) + '</span>' : '';
}

export function transportOption(id, label, key = id) {
  const { name, source } = transportDisplay(id, label);
  return '<b>' + esc(name) + '</b>' + transportKey(source ?? key);
}
