// URL fragments are only visible to the browser. Preserve exact workspace
// message and review anchors across a sign-in (including a rejected attempt).
const next = document.querySelector('form[action="/session"] input[name="next"]');
if (next && /^#(?:message-\d+|feedback:[\w-]+)$/.test(location.hash)) {
  next.value = next.value.split('#')[0] + location.hash;
}
