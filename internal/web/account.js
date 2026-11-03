// Sign-in for a shared Town. A local Town has one operator who holds the
// access key; a multi-user Town has people who sign in with GitHub or Google
// and share the same repositories, decisions and Assistant conversations.

export const providerNames = { github: "GitHub", google: "Google" };

// authHeaders sends the local access key when this browser has one. A
// signed-in browser sends nothing: its session cookie travels on its own.
export function authHeaders(token) {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// signInError reads the message Town puts in the address after a failed
// sign-in. Town only ever sends its own fixed sentences.
export function signInError(hash) {
  return new URLSearchParams(String(hash || "").replace(/^#/, "")).get("signin-error") || "";
}

// readSession asks Town how this browser signs in. A Town that cannot answer
// is treated as local, which falls back to the access-key instructions.
export async function readSession(fetcher = fetch) {
  try {
    const response = await fetcher("/api/session", { cache: "no-store" });
    if (!response.ok) return { mode: "local" };
    const session = await response.json();
    return session && typeof session === "object" ? session : { mode: "local" };
  } catch {
    return { mode: "local" };
  }
}

// whoLabel names who asked an Assistant question or made a change. Turns and
// events without an author come from the local operator.
export function whoLabel(author, { shared = false } = {}) {
  if (author) return author;
  return shared ? "Operator" : "You";
}

// account wires the header chip and the sign-in dialog.
export function account({ $, getSession, getToken }) {
  function showSignIn(message = "") {
    const session = getSession();
    if (session.mode !== "multi-user" || getToken()) {
      $("#connect-dialog").showModal();
      return;
    }
    const providers = session.providers || [];
    for (const name of Object.keys(providerNames)) {
      const link = $(`#signin-${name}`);
      if (link) link.hidden = !providers.includes(name);
    }
    $("#signin-error").textContent = message;
    if (!$("#signin-dialog").open) $("#signin-dialog").showModal();
  }
  function render() {
    const session = getSession(), user = session.user;
    $("#account").hidden = session.mode !== "multi-user";
    $("#account-name").textContent = user ? user.display : getToken() ? "Operator (access key)" : "Not signed in";
    $("#sign-out").hidden = !user;
  }
  $("#sign-out").onclick = async () => {
    $("#sign-out").disabled = true;
    try {
      await fetch("/auth/logout", { method: "POST" });
    } finally {
      location.assign("/");
    }
  };
  // Escape must not dismiss the sign-in screen into an empty, unusable Town.
  $("#signin-dialog").addEventListener("cancel", (event) => event.preventDefault());
  return { render, showSignIn };
}
