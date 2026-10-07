// The precinct's cast, drawn as inline SVG: a robot officer for each unit, the
// slop in its three states (loose, cuffed, reformed), and the vehicles on the
// street. The art carries CSS classes for its loops; scene.js places it and
// moves it. There are no image assets, and nothing here reads or writes
// state.

// ───────────── the officers ─────────────

// Each unit's robot carries the tool of its trade.
const props = {
  repo: {
    on: `<g class="prop prop-sweep"><rect x="42" y="25" width="9" height="5" rx="1.5" fill="#5c6a80"/><path d="M51 24 L60 18 L60 37 L51 31 Z" class="beam"/></g>`,
    off: `<g class="prop prop-bob"><circle cx="48" cy="28" r="5.2" fill="#e8a25c"/><circle cx="48" cy="28" r="5.2" fill="none" stroke="#ff8fb3" stroke-width="2.4" stroke-dasharray="3 2"/><circle cx="48" cy="28" r="1.8" fill="#10161f"/></g>`,
  },
  bug: `<g class="prop prop-sweep"><line x1="44" y1="32" x2="40" y2="37" stroke="#b4bfd0" stroke-width="2.4" stroke-linecap="round"/><circle cx="48" cy="26" r="5.5" fill="#3b8cff33" stroke="#e9eef6" stroke-width="2"/><circle cx="46.5" cy="24.5" r="1.4" fill="#ffffffaa"/></g>`,
  feature: `<g class="prop prop-tilt"><rect x="41" y="22" width="7" height="8" rx="2" fill="#2b3749"/><rect x="49" y="22" width="7" height="8" rx="2" fill="#2b3749"/><rect x="47" y="24" width="3" height="3" fill="#2b3749"/><circle cx="44.5" cy="29" r="2.2" fill="#7dd3fc"/><circle cx="52.5" cy="29" r="2.2" fill="#7dd3fc"/></g>`,
  simplifier: `<g class="prop prop-scales"><line x1="48" y1="17" x2="48" y2="36" stroke="#ffd23f" stroke-width="1.6"/><path d="M44 36 h8" stroke="#ffd23f" stroke-width="2"/><g class="beam-tilt"><line x1="40" y1="19" x2="56" y2="19" stroke="#ffd23f" stroke-width="1.6"/><path d="M40 19 l-3 6 h6 z M56 19 l-3 6 h6 z" fill="none" stroke="#ffd23f" stroke-width="1"/><path d="M37 25 q3 3 6 0 z M53 25 q3 3 6 0 z" fill="#ffd23f"/></g></g>`,
  hall: `<g class="prop prop-gavel"><line x1="41" y1="32" x2="50" y2="22" stroke="#a16b3f" stroke-width="2.6" stroke-linecap="round"/><rect x="46" y="15" width="10" height="7" rx="1.5" fill="#7c4a24" transform="rotate(45 51 18.5)"/></g><rect x="44" y="36" width="12" height="4" rx="1" fill="#7c4a24"/>`,
  issue: `<g class="prop prop-clipboard"><rect x="42" y="18" width="12" height="16" rx="1.5" fill="#c9a26b"/><rect x="43.5" y="20.5" width="9" height="12" fill="#f4f1de"/><rect x="45.5" y="17" width="5" height="3" rx="1" fill="#7b889d"/><path d="M45 24 h6 M45 27 h6 M45 30 h4" stroke="#7b889d" stroke-width="0.8"/></g><path class="pen" d="M50 33 l5 -7" stroke="#3b8cff" stroke-width="1.8" stroke-linecap="round"/>`,
  review: `<g class="prop prop-tube"><path d="M45 16 h7 v15 a3.5 3.5 0 0 1 -7 0 z" fill="#ffffff22" stroke="#e9eef6" stroke-width="1.4"/><path d="M45.7 24 h5.6 v7 a2.8 2.8 0 0 1 -5.6 0 z" fill="#34d399"/></g><g class="bubbles"><circle cx="47.5" cy="22" r="1"/><circle cx="49.5" cy="19" r="0.8"/><circle cx="48" cy="16" r="0.7"/></g>`,
  release: `<g class="prop prop-keys"><circle cx="47" cy="25" r="3.4" fill="none" stroke="#ffd23f" stroke-width="1.6"/><path d="M48 28 v8 h2 M48 32 h2" stroke="#ffd23f" stroke-width="1.6" fill="none"/><path d="M45 28 l-3 7 l2 0.8 M43.5 31.5 l1.8 0.7" stroke="#e9c46a" stroke-width="1.6" fill="none"/></g>`,
};

// The Magistrate and the Probation Judge sit in robes and wigs; everyone else
// wears the uniform and the cap.
const judges = new Set(["hall", "simplifier"]);

// robotCop draws one unit's officer in the state its light reports: on a case
// (working its prop), standing by, asleep through quiet hours, or stuck and
// waving under a red light.
export function robotCop(role, light = "waiting") {
  const kit = props[role];
  const prop = kit && typeof kit === "object" ? (light === "active" ? kit.on : kit.off) : kit || "";
  const asleep = light === "quiet";
  const robed = judges.has(role);
  const eyes = asleep
    ? `<path class="eyes" d="M22 21.5 h4 M30 21.5 h4" stroke="#7dd3fc" stroke-width="1.6" stroke-linecap="round"/>`
    : `<g class="eyes"><circle cx="24" cy="21.5" r="1.9" fill="#7dd3fc"/><circle cx="32" cy="21.5" r="1.9" fill="#7dd3fc"/></g>`;
  const extra = asleep
    ? `<g class="zzz" fill="#b4bfd0" font-family="monospace" font-weight="700"><text x="40" y="12" font-size="7">z</text><text x="45" y="7" font-size="8">z</text><text x="50" y="2" font-size="9">Z</text></g>`
    : light === "blocked"
      ? `<g class="help"><circle cx="46" cy="9" r="6" fill="#ff4757"/><text x="46" y="12.2" text-anchor="middle" font-family="monospace" font-weight="800" font-size="9" fill="#fff">!</text></g>`
      : "";
  const cloth = robed ? "#16171d" : "#1d3157";
  const chest = robed
    ? `<path d="M25 29 l3 6 l3 -6 z" fill="#f4f1de"/><path d="M26.5 33 h3 v3 h-3 z" fill="#f4f1de"/>`
    : `<path d="M25 31 l1 2 2.2.3-1.6 1.5.4 2.2-2-1.1-2 1.1.4-2.2-1.6-1.5 2.2-.3z" fill="#ffd23f"/><rect x="17" y="41" width="22" height="2.2" fill="#0b1017"/>`;
  const head = robed
    ? `<g class="wig" fill="#f4f1de"><circle cx="17" cy="14" r="4.5"/><circle cx="23" cy="10" r="4.5"/><circle cx="28" cy="9" r="4.5"/><circle cx="33" cy="10" r="4.5"/><circle cx="39" cy="14" r="4.5"/><circle cx="14.5" cy="20" r="3.6"/><circle cx="14.5" cy="26" r="3.4"/><circle cx="41.5" cy="20" r="3.6"/><circle cx="41.5" cy="26" r="3.4"/></g>`
    : `<path d="M14 13 Q28 1 42 13 Z" fill="#13213a"/><rect x="14" y="11" width="28" height="3.6" rx="1" fill="#0b1017"/><path d="M13 14.4 h30 l-3 3 h-24 z" fill="#05070b"/><path d="M28 5 l.9 1.8 2 .3-1.4 1.3.3 2-1.8-.9-1.8.9.3-2-1.4-1.3 2-.3z" fill="#ffd23f"/>`;
  const legs = robed
    ? `<path d="M17 41 h22 l2 11 h-26 z" fill="${cloth}"/>`
    : `<rect x="21" y="43" width="5" height="9" rx="1.5" fill="#16243d"/><rect x="30" y="43" width="5" height="9" rx="1.5" fill="#16243d"/>`;
  return `<svg class="cop cop-${light}${robed ? " judge" : ""}" viewBox="0 0 60 58" aria-hidden="true" focusable="false"><ellipse cx="28" cy="54" rx="14" ry="2.6" fill="#000" opacity=".4"/><g class="cop-body">${legs}<rect x="17" y="29" width="22" height="16" rx="4" fill="${cloth}"/>${chest}<rect x="12" y="31" width="5" height="10" rx="2.5" fill="${cloth}"/><rect x="38" y="29" width="8" height="5" rx="2.5" fill="${cloth}"/><rect x="15" y="12" width="26" height="18" rx="6" fill="#cfd8e6"/><rect x="18" y="17" width="20" height="9" rx="3.5" fill="#0b1017"/>${eyes}${head}</g>${prop}${extra}</svg>`;
}

// ───────────── the slop ─────────────

// The slop is a blob of green goo with googly eyes. A cuffed one is on its
// way to, or sitting in, the Slop Tank.
export function slopBlob({ cuffed = false } = {}) {
  const mouth = cuffed
    ? `<path d="M15 26 q5 -3 10 0" stroke="#1a2e05" stroke-width="1.6" fill="none" stroke-linecap="round"/>`
    : `<path d="M14 24 q6 5 12 0" stroke="#1a2e05" stroke-width="1.6" fill="none" stroke-linecap="round"/>`;
  const cuffs = cuffed
    ? `<g class="cuffs" fill="none" stroke="#c9d3e0" stroke-width="1.8"><circle cx="14.5" cy="33" r="3"/><circle cx="25.5" cy="33" r="3"/><path d="M17.5 33 h5" stroke-dasharray="1.4 1"/></g>`
    : "";
  return `<svg class="slop${cuffed ? " cuffed" : ""}" viewBox="0 0 40 40" aria-hidden="true" focusable="false"><g class="goo"><path d="M6 31 C2 31 2 23 7 21 C6 13 13 8 20 9 C27 7 34 13 33 20 C38 22 38 31 33 31 C33 35 29 34 28 32 C26 36 22 36 21 32 C18 35 14 35 13 32 C11 34 7 34 6 31 Z" fill="#84cc16" stroke="#3f6212" stroke-width="1.2"/><ellipse cx="13" cy="15" rx="3.5" ry="2" fill="#ecfccb" opacity=".55"/><path class="drip" d="M29 32 q1.2 4 0 6 q-1.2 -2 0 -6z" fill="#84cc16"/><g class="googly"><circle cx="15" cy="18" r="4" fill="#fff"/><circle cx="25" cy="17" r="4" fill="#fff"/><circle class="pupil" cx="15.8" cy="18.6" r="1.9" fill="#0b1017"/><circle class="pupil" cx="25.8" cy="17.6" r="1.9" fill="#0b1017"/></g>${mouth}</g>${cuffs}</svg>`;
}

// Rehabilitated slop is slop no more: a tidy citizen in a tie, with a green
// check, on its way out of the precinct.
export function reformedBlob() {
  return `<svg class="reformed" viewBox="0 0 40 40" aria-hidden="true" focusable="false"><g class="neat"><path d="M8 33 C3 33 3 22 8 18 C8 10 14 6 20 6 C27 6 32 10 32 18 C37 22 37 33 32 33 Z" fill="#7dd3fc" stroke="#0e7490" stroke-width="1.2"/><ellipse cx="14" cy="12.5" rx="4" ry="2.2" fill="#fff" opacity=".6"/><circle cx="15.5" cy="18" r="2.2" fill="#0b1017"/><circle cx="24.5" cy="18" r="2.2" fill="#0b1017"/><circle cx="16.2" cy="17.3" r=".7" fill="#fff"/><circle cx="25.2" cy="17.3" r=".7" fill="#fff"/><path d="M15 22.5 q5 4 10 0" stroke="#0b1017" stroke-width="1.6" fill="none" stroke-linecap="round"/><path d="M18.4 26 h3.2 l-1.6 1.8 z M20 27.6 l1.6 5 h-3.2 z" fill="#1d3157"/><circle cx="29" cy="28" r="4" fill="#34d399"/><path d="M27 28 l1.4 1.4 l2.6 -2.8" stroke="#05070b" stroke-width="1.3" fill="none"/></g><g class="sparkle"><path d="M34 5 l1 2.5 2.5 1-2.5 1-1 2.5-1-2.5-2.5-1 2.5-1z" fill="#fff"/><path d="M5 8 l.7 1.6 1.6.7-1.6.7-.7 1.6-.7-1.6-1.6-.7 1.6-.7z" fill="#ffd23f"/></g></svg>`;
}

// inCustody is a case the squad threw out: dismissed by the Probation Judge
// or the Magistrate. That slop sits in the Slop Tank.
export function inCustody(task) {
  return !!task && (task.stage === "declined" || task.mayoral_decision === "declined");
}

// ───────────── the motor pool ─────────────

const wheel = (cx, cy, r) => `<g class="wheel"><circle cx="${cx}" cy="${cy}" r="${r}" fill="#05070b" stroke="#7b889d" stroke-width="1.6"/><path d="M${cx - r + 2} ${cy} h${2 * r - 4} M${cx} ${cy - r + 2} v${2 * r - 4}" stroke="#7b889d" stroke-width="1"/></g>`;

// A squad car with its bar lit.
export function cruiser() {
  return `<svg class="car" viewBox="0 0 82 36" aria-hidden="true" focusable="false"><path d="M4 28 L8 19 L20 17 L28 9 L52 9 L60 17 L75 19 L78 28 Z" fill="#0d1420" stroke="#e9eef6" stroke-width="1.3"/><rect x="24" y="18" width="30" height="8" fill="#e9eef6"/><path d="M38 19.3 l.9 1.8 2 .3-1.4 1.3.3 2-1.8-.9-1.8.9.3-2-1.4-1.3 2-.3z" fill="#ffd23f"/><path d="M30 10.5 L38 10.5 L38 16.5 L23 16.5 Z" fill="#3b8cff55"/><path d="M41 10.5 L51 10.5 L58 16.5 L41 16.5 Z" fill="#3b8cff55"/><rect x="74" y="20" width="3" height="3" fill="#ffd23f"/><rect class="bar-red" x="33" y="2" width="7" height="4" rx="1.5" fill="#ff4757"/><rect class="bar-blue" x="40" y="2" width="7" height="4" rx="1.5" fill="#3b8cff"/>${wheel(20, 28, 5.5)}${wheel(62, 28, 5.5)}</svg>`;
}

// The release bus that takes the reformed out of the precinct for good.
export function bus() {
  const windows = [18, 40, 62, 84, 106, 128].map((x) => `<rect x="${x}" y="15" width="18" height="14" rx="2" fill="#13213a"/><circle class="rider" cx="${x + 9}" cy="25" r="4.2" fill="#7dd3fc"/>`).join("");
  return `<svg class="bus" viewBox="0 0 180 62" aria-hidden="true" focusable="false"><rect x="4" y="8" width="170" height="42" rx="7" fill="#e9eef6" stroke="#0b1017" stroke-width="1.5"/><rect x="4" y="34" width="170" height="7" fill="#3b8cff"/>${windows}<rect x="152" y="13" width="16" height="33" rx="2" fill="#13213a"/><rect x="10" y="9" width="96" height="5" fill="#0b1017"/><text x="58" y="13.3" text-anchor="middle" font-family="monospace" font-weight="800" font-size="4.6" fill="#ffd23f">RELEASE EXPRESS · OUTBOUND</text><text x="88" y="40.2" text-anchor="middle" font-family="monospace" font-weight="800" font-size="5" fill="#fff">SLOPCOP SQUAD</text>${wheel(36, 51, 8)}${wheel(144, 51, 8)}</svg>`;
}
