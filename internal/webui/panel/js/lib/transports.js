import { esc } from './dom.js';

const sources = new Map([
  ['anytls', 'Backhaul'],
  ['native-xtcpmux', 'Backhaul'],
  ['native-xwsmux', 'Backhaul'],
  ['native-tun-tcp', 'Backhaul'],
  ['backhaul-tun-ipx', 'Backhaul'],
  ['gretap', 'Gretap'],
  ['l2tpv3', 'VortexL2'],
]);

const gostNames = new Map([
  ['gost', 'GOST 3'],
  ['gost-direct', 'GOST 3'],
  ['gost-direct-v2', 'GOST 2'],
]);

export function transportDisplay(id, label) {
  if (id === 'easytier' || id === 'easytier-reverse') return { name: label || 'EasyTier', source: 'Easy-Mesh' };
  const easyTier = id?.match(/^easytier-(?:direct|reverse)-(tcp|udp|ws|wss)$/);
  if (easyTier) return { name: 'EasyTier / ' + easyTier[1].toUpperCase(), source: 'Easy-Mesh' };
  if (id === 'matrix') return { name: 'Matrix', source: 'Tinc' };
  if (id === 'matrix-direct') return { name: 'Matrix', source: 'Tinc · Direct' };
  if (id === 'matrix-reverse') return { name: 'Matrix', source: 'Tinc · Reverse' };
  const gostName = gostNames.get(id);
  if (gostName) return { name: gostName, source: '' };
  // Use the original transport name even for results saved by an older panel.
  const dagger = id?.replace(/^native-/, '').match(/^dagger-(http|https|xhttp|xhttps|quantum-gaming|quantum\+?|nova|dc6|tun(?:-(tcp|udp|icmp|gre|ipip|bip))?)$/);
  if (dagger) {
    const name = dagger[2] ? 'TUN / ' + dagger[2].toUpperCase() : dagger[1] === 'tun' ? 'TUN' : dagger[1];
    return { name, source: 'Dagger' };
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
