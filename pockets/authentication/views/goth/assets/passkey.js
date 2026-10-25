// Passkey controls for the authentication pages (SOC 2 S4b). It drives every
// element carrying data-auth-passkey ("signin", "register" or "stepup") through
// the pocket's passkey JSON routes; the markup contract is in the pocket README
// ("Custom views"): data-options-url, data-finish-url, data-csrf, optional
// data-return-to and data-done-url, a [data-auth-passkey-start] button, an
// optional [data-auth-passkey-name] input, an optional
// [data-auth-passkey-require-uv] checkbox (register: checked sends
// {"user_verification":"required"}), and the EMPTY live regions
// [data-auth-passkey-status] (role="status") and [data-auth-passkey-error]
// (role="alert"). The element is rendered hidden and revealed only when the
// browser has window.PublicKeyCredential, which also hides every
// [data-auth-passkey-unsupported] explanation. Options are fetched on click
// only; a step-up navigates only on aal2; messages are written as textContent.
(function () {
  "use strict";
  var PKC = window.PublicKeyCredential;
  var COPY = {
    working: "Follow your browser's prompt to use your passkey.",
    notAllowed: "The passkey request was cancelled or timed out. If you used a security key: this security key can't store a passkey; use your device or a FIDO2 key with a PIN.",
    invalidState: "This device already has a passkey for your account.",
    security: "Passkeys can't be used on this address. Open the site from its usual address and try again.",
    presenceOnly: "That passkey can't confirm a PIN or biometric, so it can't complete this check. Use a passkey that can, or add one.",
    challenge_expired: "That took too long. Please try again.",
    passkey_rejected: "That passkey wasn't accepted. Try another passkey or sign in another way.",
    uvRequired: "That passkey didn't confirm a PIN or biometric. Try again and use Face ID, Touch ID or your PIN.",
    passkey_exists: "This device already has a passkey for your account.",
    passkey_limit: "You have the maximum number of passkeys. Remove one before adding another.",
    too_many_attempts: "Too many attempts. Please wait a minute and try again.",
    registerForbidden: "Please confirm it's you first: sign in again (with a passkey if you have one), then add the passkey.",
    forbidden: "Please confirm it's you, then try again.",
    signedOut: "Your session has ended. Please sign in again.",
    generic: "Something went wrong. Please try again."
  };

  document.querySelectorAll("[data-auth-passkey-unsupported]").forEach(function (el) {
    el.hidden = Boolean(PKC);
  });
  if (!PKC || !navigator.credentials) {
    return;
  }

  function b64u(buf) {
    var s = "";
    new Uint8Array(buf).forEach(function (b) { s += String.fromCharCode(b); });
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }

  function unb64u(str) {
    var s = atob(str.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((str.length + 3) % 4));
    var out = new Uint8Array(s.length);
    for (var i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out.buffer;
  }

  function descriptor(c) {
    return Object.assign({}, c, { id: unb64u(c.id) });
  }

  // creationOptions and requestOptions prefer the browser's JSON parsers and
  // fall back to decoding the base64url members by hand.
  function creationOptions(o) {
    if (typeof PKC.parseCreationOptionsFromJSON === "function") {
      return PKC.parseCreationOptionsFromJSON(o);
    }
    var p = Object.assign({}, o, { challenge: unb64u(o.challenge) });
    p.user = Object.assign({}, o.user, { id: unb64u(o.user.id) });
    p.excludeCredentials = (o.excludeCredentials || []).map(descriptor);
    return p;
  }

  function requestOptions(o) {
    if (typeof PKC.parseRequestOptionsFromJSON === "function") {
      return PKC.parseRequestOptionsFromJSON(o);
    }
    var p = Object.assign({}, o, { challenge: unb64u(o.challenge) });
    if (o.allowCredentials) {
      p.allowCredentials = o.allowCredentials.map(descriptor);
    }
    return p;
  }

  function credentialJSON(c) {
    if (typeof c.toJSON === "function") {
      return c.toJSON();
    }
    var r = c.response;
    var out = {
      id: c.id, rawId: b64u(c.rawId), type: c.type,
      authenticatorAttachment: c.authenticatorAttachment || undefined,
      clientExtensionResults: c.getClientExtensionResults ? c.getClientExtensionResults() : {},
      response: { clientDataJSON: b64u(r.clientDataJSON) }
    };
    if (r.attestationObject) {
      out.response.attestationObject = b64u(r.attestationObject);
      out.response.transports = r.getTransports ? r.getTransports() : [];
    } else {
      out.response.authenticatorData = b64u(r.authenticatorData);
      out.response.signature = b64u(r.signature);
      if (r.userHandle) {
        out.response.userHandle = b64u(r.userHandle);
      }
    }
    return out;
  }

  async function post(url, csrf, body) {
    var res = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json", "X-CSRF-Token": csrf || "" },
      body: JSON.stringify(body)
    });
    var data = await res.json().catch(function () { return {}; });
    return { status: res.status, data: data || {} };
  }

  function wireCopy(mode, res) {
    var code = res.data.code;
    if (code && Object.prototype.hasOwnProperty.call(COPY, code)) {
      return COPY[code];
    }
    if (res.status === 403) {
      return mode === "register" ? COPY.registerForbidden : COPY.forbidden;
    }
    if (res.status === 401) {
      return mode === "signin" ? COPY.passkey_rejected : COPY.signedOut;
    }
    return res.status === 429 ? COPY.too_many_attempts : COPY.generic;
  }

  function errorCopy(e) {
    var name = e && e.name;
    if (name === "NotAllowedError") {
      return COPY.notAllowed;
    }
    if (name === "InvalidStateError") {
      return COPY.invalidState;
    }
    return name === "SecurityError" ? COPY.security : COPY.generic;
  }

  function say(root, text, isError) {
    var status = root.querySelector("[data-auth-passkey-status]");
    var alert = root.querySelector("[data-auth-passkey-error]");
    if (status) status.textContent = isError ? "" : text;
    if (alert) alert.textContent = isError ? text : "";
  }

  async function run(root, button) {
    if (root.getAttribute("aria-busy") === "true") {
      return;
    }
    var ds = root.dataset, mode = ds.authPasskey, leaving = false;
    var uv = root.querySelector("[data-auth-passkey-require-uv]");
    var requireUV = mode === "register" && Boolean(uv && uv.checked);
    root.setAttribute("aria-busy", "true");
    button.disabled = true;
    say(root, COPY.working, false);
    try {
      var begin = await post(ds.optionsUrl, ds.csrf, requireUV ? { user_verification: "required" } : {});
      if (begin.status !== 200) {
        return say(root, wireCopy(mode, begin), true);
      }
      var cred = mode === "register"
        ? await navigator.credentials.create({ publicKey: creationOptions(begin.data.public_key) })
        : await navigator.credentials.get({ publicKey: requestOptions(begin.data.public_key) });
      var body = { ceremony: begin.data.ceremony, credential: credentialJSON(cred) };
      var name = root.querySelector("[data-auth-passkey-name]");
      if (mode === "register" && name && name.value.trim()) {
        body.name = name.value.trim();
      }
      var finish = await post(ds.finishUrl, ds.csrf, body);
      if (finish.status !== 200 && finish.status !== 201) {
        var rejectedUV = requireUV && finish.status === 401 && finish.data.code === "passkey_rejected";
        return say(root, rejectedUV ? COPY.uvRequired : wireCopy(mode, finish), true);
      }
      if (mode === "stepup" && finish.data.assurance !== "aal2") {
        return say(root, COPY.presenceOnly, true);
      }
      leaving = true;
      window.location.assign(ds.returnTo || ds.doneUrl || "/");
    } catch (e) {
      say(root, errorCopy(e), true);
    } finally {
      if (!leaving) {
        root.removeAttribute("aria-busy");
        button.disabled = false;
      }
    }
  }

  document.querySelectorAll("[data-auth-passkey]").forEach(function (root) {
    var button = root.querySelector("[data-auth-passkey-start]");
    if (!button || !root.dataset.optionsUrl || !root.dataset.finishUrl) {
      return;
    }
    button.addEventListener("click", function () { run(root, button); });
    root.hidden = false;
  });
})();
