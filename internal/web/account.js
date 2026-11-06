// Sign-in for Town. People sign in with GitHub or Google and share the same
// repositories, decisions and Assistant conversations. The operator may
// instead hold the access key that bt web prints.

export const providerNames = { github: "GitHub", google: "Google" };

// authHeaders sends the operator's access key when this browser has one. A
// signed-in browser sends nothing: its session cookie travels on its own.
export function authHeaders(token) {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// signInError reads the message Town puts in the address after a failed
// sign-in. Town only ever sends its own fixed sentences.
export function signInError(hash) {
  return new URLSearchParams(String(hash || "").replace(/^#/, "")).get("signin-error") || "";
}

// readSession asks Town how this browser signs in and who it is signed in as.
// A Town that cannot answer offers no providers, and the sign-in screen says
// to reload.
export async function readSession(fetcher = fetch) {
  const none = { providers: [], user: null };
  try {
    const response = await fetcher("/api/session", { cache: "no-store" });
    if (!response.ok) return none;
    const session = await response.json();
    return session && typeof session === "object" ? session : none;
  } catch {
    return none;
  }
}

// whoLabel names who asked an Assistant question or made a change. Turns and
// events without an author come from the operator's access key.
export function whoLabel(author) {
  return author || "Operator";
}

// account wires the header chip and the sign-in dialog.
export function account({ $, getSession, getToken }) {
  // showSignIn shows the sign-in screen, or, when Town refused this browser's
  // access key, how the operator gets a fresh one.
  function showSignIn(message = "", keyRefused = false) {
    if (keyRefused) {
      $("#connect-dialog").showModal();
      return;
    }
    const providers = getSession().providers || [];
    for (const name of Object.keys(providerNames)) {
      const link = $(`#signin-${name}`);
      if (link) link.hidden = !providers.includes(name);
    }
    $("#signin-error").textContent = message || (providers.length ? "" : "Brokk Town did not say how to sign in. Reload the page to try again.");
    if (!$("#signin-dialog").open) $("#signin-dialog").showModal();
  }
  function render() {
    const session = getSession(), user = session.user;
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
