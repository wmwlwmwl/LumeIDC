(function () {
  function openCatalogSubmenu(submenu) {
    if (!submenu) return;
    submenu.classList.add('is-open');
    submenu.style.maxHeight = '0px';
    requestAnimationFrame(function () {
      submenu.style.maxHeight = submenu.scrollHeight + 'px';
    });
  }
  function closeCatalogSubmenu(submenu) {
    if (!submenu) return;
    submenu.style.maxHeight = submenu.scrollHeight + 'px';
    requestAnimationFrame(function () {
      submenu.classList.remove('is-open');
      submenu.style.maxHeight = '0px';
    });
  }
  function toggle(button, target, openClass) {
    if (!button || !target) return;
    button.addEventListener('click', function () {
      var open = target.classList.toggle(openClass);
      button.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
  }
  function catalogSearch() {
    var input = document.querySelector('#catalog-search-input');
    var headerInput = document.querySelector('#site-search-input');
    if (!input && headerInput) {
      headerInput.addEventListener('keydown', function (event) {
        if (event.key === 'Enter') window.location.href = '/products';
      });
      return;
    }
    if (!input) return;
    if (headerInput) headerInput.addEventListener('input', function () {
      input.value = headerInput.value;
      input.dispatchEvent(new Event('input'));
    });
    input.addEventListener('input', function () {
      var query = input.value.trim().toLowerCase();
      document.querySelectorAll('[data-catalog-item]').forEach(function (item) {
        var labels = Array.prototype.map.call(item.querySelectorAll('[data-catalog-label]'), function (label) {
          return label.textContent.toLowerCase();
        });
        var match = !query || labels.some(function (label) { return label.indexOf(query) !== -1; });
        item.classList.toggle('is-filtered', !match);
        if (query && match) {
          var nested = item.querySelector('[data-catalog-submenu]');
          if (nested) openCatalogSubmenu(nested);
        }
      });
    });
    if (headerInput) headerInput.addEventListener('keydown', function (event) {
      if (event.key === 'Enter') input.focus();
    });
  }
  function catalogMenu() {
    document.querySelectorAll('[data-catalog-toggle]').forEach(function (button) {
      var submenu = button.parentElement.querySelector('[data-catalog-submenu]');
      if (submenu && submenu.classList.contains('is-open')) openCatalogSubmenu(submenu);
      button.addEventListener('click', function () {
        document.querySelectorAll('[data-catalog-toggle]').forEach(function (other) {
          if (other === button) return;
          var otherSubmenu = other.parentElement.querySelector('[data-catalog-submenu]');
          if (otherSubmenu) closeCatalogSubmenu(otherSubmenu);
          other.classList.remove('is-active');
          other.classList.remove('is-open');
          other.setAttribute('aria-expanded', 'false');
        });
        if (!submenu) {
          var emptyOpen = button.classList.toggle('is-open');
          button.setAttribute('aria-expanded', emptyOpen ? 'true' : 'false');
          return;
        }
        var open = !submenu.classList.contains('is-open');
        if (open) openCatalogSubmenu(submenu);
        else closeCatalogSubmenu(submenu);
        button.classList.toggle('is-active', open);
        button.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
    });
  }
  document.addEventListener('DOMContentLoaded', function () {
    toggle(document.querySelector('[data-site-menu]'), document.querySelector('[data-site-header]'), 'is-open');
    toggle(document.querySelector('[data-admin-menu]'), document.querySelector('[data-admin-shell]'), 'is-open');
    catalogSearch();
    catalogMenu();
  });
}());
