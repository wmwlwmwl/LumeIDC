(function () {
  function toggle(button, target, openClass) {
    if (!button || !target) return;
    button.addEventListener('click', function () {
      var open = target.classList.toggle(openClass);
      button.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
  }
  document.addEventListener('DOMContentLoaded', function () {
    toggle(document.querySelector('[data-site-menu]'), document.querySelector('[data-site-header]'), 'is-open');
    toggle(document.querySelector('[data-admin-menu]'), document.querySelector('[data-admin-shell]'), 'is-open');
  });
}());
