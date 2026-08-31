(function () {
  'use strict';

  /* ---------- 目录菜单（采购中心） ---------- */
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

  /* ---------- Toast 轻提示 ---------- */
  var toastRegion = null;
  function ensureToastRegion() {
    if (toastRegion) return toastRegion;
    toastRegion = document.createElement('div');
    toastRegion.className = 'ui-toast-region';
    toastRegion.setAttribute('aria-live', 'polite');
    document.body.appendChild(toastRegion);
    return toastRegion;
  }
  function toast(msg, kind) {
    var region = ensureToastRegion();
    var el = document.createElement('div');
    el.className = 'ui-toast' + (kind ? ' ui-toast-' + kind : '');
    el.textContent = msg;
    region.appendChild(el);
    requestAnimationFrame(function () { el.classList.add('is-in'); });
    setTimeout(function () {
      el.classList.remove('is-in');
      setTimeout(function () { el.remove(); }, 300);
    }, 2200);
  }

  /* ---------- 确认弹窗（替代原生 confirm） ---------- */
  var confirmBox = null;
  function ensureConfirmBox() {
    if (confirmBox) return confirmBox;
    confirmBox = document.createElement('div');
    confirmBox.className = 'ui-confirm';
    confirmBox.innerHTML =
      '<div class="ui-confirm-card" role="dialog" aria-modal="true" aria-labelledby="uiConfirmTitle">' +
      '<h3 class="ui-confirm-title" id="uiConfirmTitle"></h3>' +
      '<p class="ui-confirm-msg"></p>' +
      '<div class="ui-confirm-actions">' +
      '<button type="button" class="ui-btn ui-btn-secondary ui-btn-small" data-ui-cancel>取消</button>' +
      '<button type="button" class="ui-btn ui-btn-small" data-ui-ok>确认</button>' +
      '</div></div>';
    document.body.appendChild(confirmBox);
    confirmBox.addEventListener('click', function (e) {
      if (e.target === confirmBox || e.target.closest('[data-ui-cancel]')) closeConfirm(false);
      else if (e.target.closest('[data-ui-ok]')) closeConfirm(true);
    });
    return confirmBox;
  }
  var confirmResolve = null;
  var lastFocus = null;
  function openConfirm(msg, okLabel) {
    ensureConfirmBox();
    lastFocus = document.activeElement;
    confirmBox.querySelector('.ui-confirm-msg').textContent = msg;
    confirmBox.querySelector('[data-ui-ok]').textContent = okLabel || '确认';
    confirmBox.classList.add('is-open');
    document.body.classList.add('ui-modal-lock');
    confirmBox.querySelector('[data-ui-ok]').focus();
    return new Promise(function (resolve) { confirmResolve = resolve; });
  }
  function closeConfirm(result) {
    if (!confirmBox || !confirmBox.classList.contains('is-open')) return;
    confirmBox.classList.remove('is-open');
    document.body.classList.remove('ui-modal-lock');
    if (confirmResolve) { confirmResolve(result); confirmResolve = null; }
    if (lastFocus && lastFocus.focus) lastFocus.focus();
  }

  /* ---------- 表单提交：确认 + 防重复 ---------- */
  function bindFormBehavior() {
    // data-confirm：提交前弹出确认框
    document.addEventListener('submit', function (e) {
      var form = e.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (form.dataset.confirmed === '1') { form.dataset.confirmed = ''; return; }
      var msg = form.getAttribute('data-confirm');
      if (!msg) return;
      e.preventDefault();
      openConfirm(msg, '确认执行').then(function (ok) {
        if (!ok) return;
        form.dataset.confirmed = '1';
        // 确认后再走一次完整的提交流程（含锁按钮）
        form.requestSubmit ? form.requestSubmit() : form.submit();
      });
    }, false);

    // 提交时锁定按钮，防止重复提交；其他监听器 preventDefault 时不锁
    document.addEventListener('submit', function (e) {
      if (e.defaultPrevented) return;
      var btn = e.submitter;
      if (!btn) {
        btn = e.target.querySelector('button[type="submit"]:not([form]), input[type="submit"]:not([form])');
      }
      if (!btn || btn.disabled) return;
      btn.dataset.locked = '1';
      btn.dataset.origHtml = btn.innerHTML;
      btn.style.minWidth = btn.offsetWidth + 'px';
      btn.classList.add('is-loading');
      btn.disabled = true;
      btn.innerHTML = '处理中…';
    }, false);

    // bfcache 返回时恢复按钮
    window.addEventListener('pageshow', function () {
      document.querySelectorAll('button[data-locked]').forEach(function (b) {
        b.disabled = false;
        b.classList.remove('is-loading');
        if (b.dataset.origHtml) b.innerHTML = b.dataset.origHtml;
        delete b.dataset.locked;
      });
    });
  }

  /* ---------- 复制到剪贴板 ---------- */
  function bindCopy() {
    document.addEventListener('click', function (e) {
      var el = e.target.closest('[data-copy]');
      if (!el) return;
      var text = el.getAttribute('data-copy') || el.textContent.trim();
      if (!text || !navigator.clipboard) return;
      navigator.clipboard.writeText(text).then(function () {
        toast('已复制到剪贴板');
      }, function () { /* 剪贴板不可用（非安全上下文）时静默 */ });
    });
  }

  /* ---------- 表格快速筛选 ---------- */
  function bindTableFilter() {
    document.querySelectorAll('input[data-table-filter]').forEach(function (input) {
      var selector = input.getAttribute('data-table-filter');
      var table = selector ? document.querySelector(selector) : null;
      if (!table) return;
      var tbody = table.tBodies[0];
      if (!tbody) return;
      var emptyRow = null;
      input.addEventListener('input', function () {
        var q = input.value.trim().toLowerCase();
        var visible = 0;
        Array.prototype.forEach.call(tbody.rows, function (row) {
          if (row.classList.contains('filter-empty-row')) return;
          var hit = !q || row.textContent.toLowerCase().indexOf(q) !== -1;
          row.style.display = hit ? '' : 'none';
          if (hit) visible++;
        });
        // 空结果提示行
        if (!emptyRow) {
          emptyRow = document.createElement('tr');
          emptyRow.className = 'filter-empty-row';
          emptyRow.innerHTML = '<td colspan="99" class="admin-table-empty">没有匹配的结果</td>';
        }
        if (visible === 0 && q) {
          if (!emptyRow.parentNode) tbody.appendChild(emptyRow);
        } else if (emptyRow.parentNode) {
          emptyRow.remove();
        }
      });
      // Esc 清空
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') { input.value = ''; input.dispatchEvent(new Event('input')); }
      });
    });
  }

  /* ---------- details 手风琴：同一操作区只保留一个展开 ---------- */
  function bindDetailsAccordion() {
    document.addEventListener('toggle', function (e) {
      var d = e.target;
      if (!(d instanceof HTMLDetailsElement) || !d.open) return;
      var scope = d.closest('.admin-type-actions, .td-actions, .admin-panel-header, .admin-panel-body');
      if (!scope) return;
      scope.querySelectorAll('details[open]').forEach(function (other) {
        if (other !== d) other.open = false;
      });
    }, true);
    // 点击空白处收起弹出面板
    document.addEventListener('click', function (e) {
      if (e.target.closest('details')) return;
      document.querySelectorAll('details[open].admin-action-pop').forEach(function (d) { d.open = false; });
    });
  }

  /* ---------- Esc 关闭弹层 ---------- */
  function bindEscape() {
    document.addEventListener('keydown', function (e) {
      if (e.key !== 'Escape') return;
      closeConfirm(false);
      var modal = document.getElementById('cfgModal');
      if (modal && modal.style.display !== 'none' && typeof closeCfgModal === 'function') closeCfgModal();
    });
  }

  /* ---------- 成功提示自动淡出 ---------- */
  function bindAutoDismiss() {
    document.querySelectorAll('.ui-alert-success').forEach(function (el) {
      setTimeout(function () {
        el.classList.add('is-fading');
        setTimeout(function () { el.remove(); }, 400);
      }, 4000);
    });
  }

  /* ---------- 目录导入：全选 ---------- */
  function bindSelectAll() {
    var master = document.getElementById('selectAllImport');
    if (!master) return;
    master.addEventListener('change', function () {
      document.querySelectorAll('input[name="import"]:not(:disabled)').forEach(function (cb) {
        cb.checked = master.checked;
      });
    });
  }

  /* ---------- 数字输入框：滚轮不改变数值（防误触） ---------- */
  function bindNumberWheelGuard() {
    document.addEventListener('wheel', function (e) {
      var t = e.target;
      if (t && t.tagName === 'INPUT' && t.type === 'number') t.blur();
    }, { passive: true });
  }

  document.addEventListener('DOMContentLoaded', function () {
    toggle(document.querySelector('[data-site-menu]'), document.querySelector('[data-site-header]'), 'is-open');
    toggle(document.querySelector('[data-admin-menu]'), document.querySelector('[data-admin-shell]'), 'is-open');
    catalogSearch();
    catalogMenu();
    bindFormBehavior();
    bindCopy();
    bindTableFilter();
    bindDetailsAccordion();
    bindEscape();
    bindAutoDismiss();
    bindSelectAll();
    bindNumberWheelGuard();
  });
}());
