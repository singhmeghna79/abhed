// Screening rules for access requests.
//
// These decide who is auto-approved. They are worth having and they are not a
// security boundary: a determined attacker buys a domain for a few dollars and
// passes every one of them. What contains an approved user is the sandbox tier
// and the deny rules in deploy/config.json — these rules only reduce the volume
// of obvious junk and make abuse cost something.
//
// Scored rather than pass/fail, so one weak signal does not reject a real
// person and one strong signal does not admit a bot.

// Addresses that exist to be thrown away. The list is the common ones; it is
// not exhaustive and does not need to be, because it is one signal of several.
const DISPOSABLE = new Set([
  "mailinator.com", "guerrillamail.com", "10minutemail.com", "tempmail.com",
  "throwawaymail.com", "yopmail.com", "trashmail.com", "sharklasers.com",
  "getnada.com", "temp-mail.org", "fakeinbox.com", "dispostable.com",
  "maildrop.cc", "mintemail.com", "mytemp.email", "spamgourmet.com",
  "tempinbox.com", "emailondeck.com", "burnermail.io", "mohmal.com",
]);

// Consumer providers. Not a rejection — plenty of real developers use them —
// but not corroborating either.
const FREEMAIL = new Set([
  "gmail.com", "googlemail.com", "yahoo.com", "hotmail.com", "outlook.com",
  "live.com", "aol.com", "icloud.com", "me.com", "proton.me", "protonmail.com",
  "gmx.com", "mail.com", "zoho.com", "yandex.com", "qq.com", "163.com",
]);

const MAX = { name: 120, email: 254, company: 120, use: 2000 };

// Names that get impersonated. Used only to catch them appearing where they
// do not belong — a subdomain — not as an allow list.
const KNOWN = [
  "google", "microsoft", "apple", "amazon", "ibm", "oracle", "cisco",
  "siemens", "cloudflare", "openai", "anthropic", "meta", "netflix",
  "salesforce", "sap", "intel", "nvidia", "adobe", "vmware", "redhat",
];

// Prompt-injection shapes. The use case is read by a human and may later be
// read by a model; text that tries to issue instructions is not a use case.
const INJECTION = [
  /ignore\s+(all\s+)?(previous|prior|above)\s+instructions/i,
  /disregard\s+(all\s+)?(previous|prior|the\s+above)/i,
  /you\s+are\s+now\s+(a|an)\s/i,
  /\bsystem\s*prompt\b/i,
  /<\s*\/?\s*(script|iframe|object|embed)\b/i,
  /\bcurl\b[^\n]{0,40}\|\s*(sh|bash)\b/i,
  /\b(exfiltrate|reverse\s+shell|privilege\s+escalation)\b/i,
];

const LINKY = /(https?:\/\/|www\.)/gi;

export function screen(req) {
  const reasons = [];
  let score = 0;

  const name = (req.name || "").trim();
  const email = (req.email || "").trim().toLowerCase();
  const company = (req.company || "").trim();
  const use = (req.use || "").trim();

  // --- hard rejections: malformed or hostile -----------------------------
  if (!name || !email) return verdict("reject", ["name and email are required"], 0);
  for (const [f, v] of [["name", name], ["email", email],
                        ["company", company], ["use", use]]) {
    if (v.length > MAX[f]) return verdict("reject", [`${f} exceeds ${MAX[f]} characters`], 0);
  }
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]{2,}$/.test(email)) {
    return verdict("reject", ["email address is not well formed"], 0);
  }
  // Control characters in a field that ends up in an email body.
  if (/[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(name + email + company + use)) {
    return verdict("reject", ["control characters in submitted fields"], 0);
  }
  for (const re of INJECTION) {
    if (re.test(use) || re.test(name) || re.test(company)) {
      return verdict("reject", ["submission contains instruction-shaped text"], 0);
    }
  }

  const domain = email.split("@")[1] || "";
  if (DISPOSABLE.has(domain)) {
    return verdict("reject", [`${domain} is a disposable address provider`], 0);
  }

  // Non-ASCII in a domain is a homoglyph attack in practice: "sіemens.com"
  // with a Cyrillic i auto-approved as a corporate domain. A real corporate
  // sender uses the punycode form, which is ASCII, so rejecting non-ASCII
  // here costs nothing and closes the lookalike class outright.
  if (!/^[\x00-\x7F]*$/.test(email)) {
    return verdict("reject", ["email contains non-ASCII characters"], 0);
  }

  // The registrable domain is the last two labels; anything before them is a
  // subdomain the sender controls. "ibm.com.evil.co" is evil.co wearing ibm's
  // name, and it auto-approved before this check existed.
  const labels = domain.split(".");
  const registrable = labels.slice(-2).join(".");
  const claimsKnownBrand = labels.length > 2 &&
    KNOWN.some((k) => labels.slice(0, -2).join(".").includes(k));
  if (claimsKnownBrand) {
    return verdict("reject",
      [`${domain} puts a known name in a subdomain of ${registrable}`], 0);
  }

  // --- signals -----------------------------------------------------------

  // A domain one or two edits from a well-known brand is a lookalike:
  // "rnicrosoft.com" reads as Microsoft at a glance and is a different
  // company. This cannot be a rejection — plenty of real small companies sit
  // within two edits of a big name by coincidence — so it removes the
  // corporate bonus and sends the request to a human instead.
  const stem = registrable.split(".")[0];
  const lookalike = KNOWN.some((k) => k !== stem && editDistance(k, stem) <= 2);
  if (lookalike) {
    score -= 4;
    reasons.push(`${registrable} is within two edits of a well-known name`);
  }

  const corporate = !FREEMAIL.has(domain);
  if (corporate) { score += 3; reasons.push(`${domain} is not a consumer provider`); }
  else { reasons.push(`${domain} is a consumer provider`); }

  if (company) { score += 1; reasons.push("company named"); }

  // A real use case is a sentence, not a word. Length is a weak proxy and is
  // treated as one.
  const words = use ? use.split(/\s+/).length : 0;
  if (words >= 12) { score += 3; reasons.push(`use case is ${words} words`); }
  else if (words >= 5) { score += 1; reasons.push(`use case is short (${words} words)`); }
  else { reasons.push(words ? `use case is ${words} words` : "no use case given"); }

  // The company and the email domain agreeing is the strongest cheap signal
  // that the person is who they say they are.
  if (company && corporate) {
    const c = company.toLowerCase().replace(/[^a-z0-9]/g, "");
    const d = domain.split(".")[0].replace(/[^a-z0-9]/g, "");
    if (c && d && (c.includes(d) || d.includes(c))) {
      score += 2; reasons.push("company matches the email domain");
    }
  }

  // Links in a use case are how link spam arrives.
  const links = (use.match(LINKY) || []).length;
  if (links > 0) { score -= 4; reasons.push(`${links} link(s) in the use case`); }

  // Name that is an address, or gibberish with no vowels, is bot-shaped.
  if (/@/.test(name)) { score -= 4; reasons.push("name contains an address"); }
  if (name.length > 3 && !/[aeiou]/i.test(name)) {
    score -= 4; reasons.push("name has no vowels");
  }
  // A single-word name is not disqualifying, but it is not corroborating.
  if (!/\s/.test(name)) { score -= 1; reasons.push("single-word name"); }

  // A use case is required for auto-approval, not merely scored. Three of the
  // first test cases auto-approved on the corporate-domain bonus alone —
  // including link spam and a gibberish name — which is exactly the failure
  // this screening exists to prevent. Domain reputation is a weak signal and
  // must not be sufficient on its own.
  // A use case is required, but how much of one scales with the rest of the
  // evidence: a named employee writing from a domain that matches their
  // company has already corroborated themselves, and holding them to a word
  // count would reject the most credible requests on the page. Someone with
  // no other signal has to say more.
  const floor = score >= 7 ? 6 : 12;
  let action;
  if (words < floor) {
    action = "review";
    reasons.push(words === 0
      ? "auto-approval needs a stated use case"
      : `use case is under ${floor} words for this level of evidence`);
  } else if (score >= 7) {
    action = "approve";
  } else {
    action = "review";
  }
  return verdict(action, reasons, score);
}

// Levenshtein distance, capped: anything past the cap is "far enough".
function editDistance(a, b) {
  if (Math.abs(a.length - b.length) > 2) return 99;
  const prev = Array(b.length + 1).fill(0).map((_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let diag = prev[0];
    prev[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const tmp = prev[j];
      prev[j] = Math.min(
        prev[j] + 1,
        prev[j - 1] + 1,
        diag + (a[i - 1] === b[j - 1] ? 0 : 1),
      );
      diag = tmp;
    }
  }
  return prev[b.length];
}

function verdict(action, reasons, score) {
  return { action, score, reasons };
}
