import { esc } from './dom.js';

const sources = new Map([
  ['anytls', 'Backhaul'],
  ['gretap', 'Gretap'],
  ['l2tpv3', 'VortexL2'],
  ['easytier', 'VortexL2'],
  ['gost-direct', 'GOST'],
]);

export function transportDisplay(id, label) {
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
