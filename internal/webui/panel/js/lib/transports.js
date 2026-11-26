import { esc } from './dom.js';

const sources = new Map([
  ['anytls', 'Backhaul'],
  ['native-xtcpmux', 'Backhaul'],
  ['native-xwsmux', 'Backhaul'],
  ['native-tun-tcp', 'Backhaul'],
  ['gretap', 'Gretap'],
  ['l2tpv3', 'VortexL2'],
  ['easytier', 'VortexL2'],
  ['gost-direct', 'GOST'],
]);

export function transportDisplay(id, label) {
  // Use the original transport name even for results saved by an older panel.
  const dagger = id?.replace(/^native-/, '').match(/^dagger-(http|https|xhttp|quantum\+?|nova|dc6|tun(?:-(tcp|udp|icmp|gre|ipip|bip))?)$/);
  if (dagger) return { name: dagger[2] ? 'tun / ' + dagger[2] : dagger[1], source: 'Dagger' };
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
  return '<b>' + esc(name) + '</b>' + transportKey(source || key);
}
