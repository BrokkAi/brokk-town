import { positions } from "./town.js";

// Scenery is painted once per town. Animation only reads committed worker state.
export function seededRandom(seed) {
  let n = 2166136261;
  for (const c of String(seed)) n = Math.imul(n ^ c.charCodeAt(0), 16777619);
  return () => {
    n = (Math.imul(n, 1664525) + 1013904223) >>> 0;
    return n / 4294967296;
  };
}
export function easeDelivery(progress) {
  const p = Math.max(0, Math.min(1, progress));
  return p * p * (3 - 2 * p);
}
export function workerPose(role, time, motion = true) {
  const index = ["bug", "issue", "review", "release", "repo"].indexOf(role);
  const t = motion ? time / 1000 + Math.max(0, index) * 1.73 : 0;
  const cycle = t % 7,
    walking = motion && cycle < 2.8;
  return {
    x: 72 + (walking ? Math.sin((cycle / 2.8) * Math.PI) * 31 : 0),
    y: 74 + (walking ? Math.abs(Math.sin(t * 13)) * 3 : 0),
    tilt: motion
      ? Math.sin(t * (walking ? 13 : 7)) * (walking ? 0.06 : 0.13)
      : 0,
    working: !walking,
    phase: t,
  };
}
function oval(c, x, y, rx, ry, color) {
  c.fillStyle = color;
  c.beginPath();
  c.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2);
  c.fill();
}
function polygon(c, points, color) {
  c.fillStyle = color;
  c.beginPath();
  points.forEach(([x, y], i) => (i ? c.lineTo(x, y) : c.moveTo(x, y)));
  c.closePath();
  c.fill();
}
function tree(c, x, y, s, pine) {
  c.save();
  c.translate(x, y);
  c.scale(s, s);
  oval(c, 12, 7, 31, 11, "#132c2480");
  c.fillStyle = "#5b4a31";
  c.fillRect(-4, -37, 9, 40);
  c.fillStyle = "#a18a55";
  c.fillRect(-4, -28, 3, 28);
  if (pine) {
    for (let i = 0; i < 3; i++) {
      const yy = -30 - i * 19,
        width = 31 - i * 6;
      polygon(
        c,
        [
          [-width, yy + 8],
          [0, yy - 37],
          [width, yy + 8],
          [9, yy + 15],
          [-8, yy + 12],
        ],
        "#203e2b",
      );
      polygon(
        c,
        [
          [-width, yy + 6],
          [-4, yy - 30],
          [-1, yy + 3],
          [-12, yy + 9],
        ],
        "#4c7140",
      );
      polygon(
        c,
        [
          [-width + 5, yy + 1],
          [-4, yy - 30],
          [-12, yy - 3],
        ],
        "#729051",
      );
    }
  } else {
    const contour = [
      [-32, -23],
      [-42, -40],
      [-36, -62],
      [-23, -65],
      [-15, -81],
      [8, -88],
      [29, -74],
      [29, -64],
      [43, -53],
      [40, -33],
      [24, -18],
      [4, -15],
      [-10, -21],
    ];
    polygon(c, contour, "#294d30");
    polygon(
      c,
      [
        [-32, -32],
        [-40, -44],
        [-33, -61],
        [-18, -65],
        [-13, -78],
        [8, -84],
        [26, -71],
        [26, -62],
        [9, -51],
        [-1, -31],
        [-20, -24],
      ],
      "#527740",
    );
    polygon(
      c,
      [
        [-32, -47],
        [-24, -59],
        [-13, -61],
        [-8, -75],
        [8, -79],
        [21, -70],
        [7, -60],
        [0, -44],
        [-15, -37],
      ],
      "#779450",
    );
    c.fillStyle = "#a6ad65";
    c.fillRect(-20, -58, 5, 3);
    c.fillRect(-7, -70, 7, 3);
    c.fillRect(-27, -45, 4, 3);
    c.fillStyle = "#385934";
    c.fillRect(17, -40, 8, 4);
    c.fillRect(2, -25, 6, 3);
  }
  c.restore();
}
function rock(c, x, y, s) {
  c.save();
  c.translate(x, y);
  c.scale(s, s);
  oval(c, 4, 5, 17, 6, "#162c2266");
  polygon(
    c,
    [
      [-16, 3],
      [-12, -10],
      [2, -16],
      [14, -9],
      [18, 4],
      [5, 9],
    ],
    "#697b64",
  );
  polygon(
    c,
    [
      [-12, -10],
      [2, -16],
      [14, -9],
      [4, -2],
      [-16, 3],
    ],
    "#9aab87",
  );
  polygon(
    c,
    [
      [4, -2],
      [14, -9],
      [18, 4],
      [5, 9],
    ],
    "#52634f",
  );
  c.restore();
}
function road(c) {
  c.beginPath();
  c.moveTo(-20, 330);
  c.lineTo(1150, 330);
  c.moveTo(180, 230);
  c.lineTo(180, 540);
  c.lineTo(960, 540);
  c.moveTo(555, 220);
  c.lineTo(555, 550);
  c.moveTo(930, 220);
  c.lineTo(930, 550);
  c.lineTo(1150, 550);
}
function onRoad(x, y) {
  return (
    Math.abs(y - 330) < 15 ||
    (y > 230 && y < 545 && Math.abs(x - 180) < 15) ||
    (y > 220 &&
      y < 550 &&
      (Math.abs(x - 555) < 15 || Math.abs(x - 930) < 15)) ||
    (x > 180 && Math.abs(y - 540) < 15)
  );
}
export function landscape(seed) {
  const canvas = document.createElement("canvas");
  canvas.width = 1120;
  canvas.height = 680;
  const c = canvas.getContext("2d"),
    rand = seededRandom(seed);
  const light = c.createLinearGradient(0, 0, 850, 680);
  light.addColorStop(0, "#506644");
  light.addColorStop(0.5, "#3c5739");
  light.addColorStop(1, "#2b4834");
  c.fillStyle = light;
  c.fillRect(0, 0, 1120, 680);
  for (let i = 0; i < 95; i++)
    oval(
      c,
      rand() * 1120,
      rand() * 680,
      30 + rand() * 95,
      10 + rand() * 30,
      i % 2 ? "#74915b0c" : "#182f270d",
    );
  for (let i = 0; i < 2900; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    c.fillStyle = ["#89a46339", "#243e2e55", "#b4b87528", "#77945240"][i % 4];
    c.fillRect(x, y, 1 + rand() * 3, 1 + rand() * 2);
    if (i % 7 === 0) {
      c.fillRect(x + 2, y - 3, 1, 4);
      c.fillRect(x - 2, y - 2, 1, 3);
    }
  }
  c.lineCap = "round";
  c.lineJoin = "round";
  road(c);
  c.strokeStyle = "#203b2977";
  c.lineWidth = 39;
  c.stroke();
  c.strokeStyle = "#9a8c63";
  c.lineWidth = 30;
  c.stroke();
  c.strokeStyle = "#baaa7833";
  c.lineWidth = 21;
  c.stroke();
  for (let i = 0; i < 4300; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    if (onRoad(x, y)) {
      c.fillStyle = i % 2 ? "#544f3938" : "#e9dba445";
      c.fillRect(x, y, 1 + rand() * 3, 1.5);
    }
  }
  // Little lawns and paving beneath the original building sprites.
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (role === "outside") continue;
    oval(c, x, y + 62, 110, 33, "#273f2b55");
    oval(c, x - 8, y + 57, 98, 25, "#81916420");
    for (let i = 0; i < 5; i++) {
      c.fillStyle = i % 2 ? "#909072" : "#a7a07d";
      c.fillRect(x - 8 + (i % 2) * 2, y + 86 + i * 7, 15, 4);
    }
  }
  const trees = [
    [28, 99, 1.1, 1],
    [75, 66, 0.85, 0],
    [330, 112, 0.82, 0],
    [387, 142, 0.66, 1],
    [748, 103, 0.85, 1],
    [790, 85, 0.68, 0],
    [1093, 145, 1, 0],
    [1066, 208, 0.68, 1],
    [22, 240, 0.85, 1],
    [364, 288, 0.65, 0],
    [746, 260, 0.7, 0],
    [35, 439, 0.9, 0],
    [67, 479, 0.68, 1],
    [354, 452, 0.75, 1],
    [391, 424, 0.63, 0],
    [733, 463, 0.88, 0],
    [1083, 457, 1, 1],
    [1033, 486, 0.65, 0],
    [116, 628, 0.85, 0],
    [335, 645, 1, 1],
    [392, 633, 0.73, 1],
    [780, 634, 0.8, 0],
    [846, 644, 0.7, 1],
    [1076, 636, 1.05, 0],
  ];
  trees.sort((a, b) => a[1] - b[1]).forEach((v) => tree(c, ...v));
  for (const [x, y, s] of [
    [82, 215, 0.55],
    [409, 178, 0.6],
    [781, 191, 0.65],
    [1020, 65, 0.7],
    [57, 575, 0.9],
    [414, 503, 0.6],
    [698, 591, 0.8],
    [1025, 598, 0.65],
    [685, 116, 0.4],
    [350, 357, 0.5],
  ])
    rock(c, x, y, s);
  for (const [x, y] of [
    [304, 230],
    [685, 225],
    [91, 546],
    [422, 598],
    [691, 396],
    [1009, 380],
    [467, 85],
  ]) {
    for (let i = 0; i < 11; i++) {
      const xx = x + rand() * 32,
        yy = y + rand() * 15;
      c.fillStyle = "#6c8a44";
      c.fillRect(xx, yy, 1, 4);
      c.fillStyle = i % 3 ? "#cbc48a" : "#b79ab1";
      c.fillRect(xx - 1, yy - 1, 3, 2);
    }
  }
  // Fence posts add scale without enclosing the delivery routes.
  for (const [x, y] of [
    [270, 45],
    [600, 608],
    [42, 371],
  ]) {
    c.strokeStyle = "#544b32";
    c.lineWidth = 4;
    c.beginPath();
    c.moveTo(x, y);
    c.lineTo(x + 80, y);
    c.stroke();
    c.strokeStyle = "#ad9c67";
    c.lineWidth = 2;
    c.beginPath();
    c.moveTo(x, y - 3);
    c.lineTo(x + 80, y - 3);
    c.stroke();
    for (let i = 0; i < 5; i++) {
      c.fillStyle = "#9d8a5c";
      c.fillRect(x + i * 20 - 2, y - 10, 5, 17);
      c.fillStyle = "#c8b47a";
      c.fillRect(x + i * 20 - 2, y - 10, 2, 13);
    }
  }
  const shade = c.createRadialGradient(560, 295, 170, 560, 340, 680);
  shade.addColorStop(0, "#091c1500");
  shade.addColorStop(1, "#091c154b");
  c.fillStyle = shade;
  c.fillRect(0, 0, 1120, 680);
  return canvas;
}

export function drawWorking(c, role, x, y, now, motion, paintWorker) {
  const pose = workerPose(role, now, motion),
    t = pose.phase;
  oval(
    c,
    x,
    y + 66,
    99,
    25,
    `rgba(193,227,120,${motion ? 0.1 + Math.sin(t * 2) * 0.025 : 0.1})`,
  );
  if (role === "issue" || role === "release") {
    for (let i = 0; i < 4; i++) {
      const p = motion ? (t * 0.23 + i / 4) % 1 : i / 4;
      oval(
        c,
        x - 28 + p * 20,
        y - 62 - p * 42,
        4 + p * 9,
        3 + p * 6,
        `rgba(199,211,171,${(1 - p) * 0.27})`,
      );
    }
  }
  if (role === "review" || role === "repo") {
    const angle = motion ? Math.sin(t * 0.65) * 0.45 : 0;
    c.save();
    c.translate(x + 10, y - 35);
    c.rotate(angle);
    polygon(
      c,
      [
        [0, 0],
        [48, -39],
        [65, -18],
      ],
      "#d3e9a722",
    );
    c.restore();
  }
  const wx = x + pose.x,
    wy = y + pose.y;
  oval(c, wx + 3, wy + 24, 19, 5, "#14271fa0");
  c.save();
  c.translate(wx, wy);
  c.rotate(pose.tilt);
  paintWorker(role === "review" || role === "repo" ? 5 : 2);
  c.restore();
  if (pose.working || !motion) {
    for (let i = 0; i < 4; i++) {
      const p = motion ? (t * 0.9 + i / 4) % 1 : i / 4;
      c.fillStyle =
        role === "bug"
          ? `rgba(192,233,130,${1 - p})`
          : `rgba(249,216,126,${1 - p})`;
      const xx = wx + 18 + Math.sin(i * 2.4) * p * 16,
        yy = wy - 4 - p * 23;
      c.fillRect(xx, yy, 3, 3);
    }
  }
}
