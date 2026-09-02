(function () {
  'use strict';

  /* ---------- 目录菜单（采购中心） ---------- */
  function openCatalogSubmenu(submenu) {
    if (!submenu) return;
    submenu.classList.add('is-open');
    submenu.style.maxHeight = 'none';
    submenu.style.overflow = 'visible';
  }
  function closeCatalogSubmenu(submenu) {
    if (!submenu) return;
    submenu.classList.remove('is-open');
    submenu.style.removeProperty('max-height');
    submenu.style.removeProperty('overflow');
  }
  function toggle(button, target, openClass) {
    if (!button || !target) return;
    button.addEventListener('click', function () {
      var open = target.classList.toggle(openClass);
      button.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
    document.addEventListener('click', function (e) {
      if (!target.classList.contains(openClass)) return;
      if (e.target.closest('[data-site-menu]') || e.target.closest('[data-site-header]')) return;
      target.classList.remove(openClass);
      button.setAttribute('aria-expanded', 'false');
    });
  }
  function catalogSearch() {
    var input = document.querySelector('#catalog-search-input');
    if (!input) return;
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
  function openConfirm(msg, okLabel, danger) {
    ensureConfirmBox();
    lastFocus = document.activeElement;
    confirmBox.querySelector('.ui-confirm-msg').textContent = msg;
    var ok = confirmBox.querySelector('[data-ui-ok]');
    ok.textContent = okLabel || '确认';
    ok.classList.toggle('ui-btn-danger', !!danger);
    ok.classList.toggle('ui-btn-secondary', false);
    confirmBox.classList.add('is-open');
    document.body.classList.add('ui-modal-lock');
    ok.focus();
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
    // 兼容供应商组件的旧式内联确认，统一接入本站确认弹窗。
    document.querySelectorAll('form[onsubmit]').forEach(function (form) {
      var inline = form.getAttribute('onsubmit') || '';
      var match = inline.match(/confirm\(['"]([^'"]*)['"]\)/);
      if (!match) return;
      form.setAttribute('data-confirm', match[1]);
      form.removeAttribute('onsubmit');
    });
    // data-confirm：提交前弹出确认框
    document.addEventListener('submit', function (e) {
      var form = e.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (form.dataset.confirmed === '1') { form.dataset.confirmed = ''; return; }
      var msg = form.getAttribute('data-confirm');
      if (!msg) return;
      e.preventDefault();
      var danger = form.hasAttribute('data-danger');
      openConfirm(msg, '确认执行', danger).then(function (ok) {
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
      // 命中计数徽标（放筛选框最右侧）
      var countEl = null;
      var bar = input.closest('.list-filter');
      if (bar) {
        countEl = document.createElement('span');
        countEl.className = 'list-filter-count';
        countEl.setAttribute('aria-hidden', 'true');
        bar.appendChild(countEl);
      }
      // 空状态占位行不计入数据行（admin-table-empty / filter-empty-row）
      function isDataRow(row) {
        var cls = row.className || '';
        return cls.indexOf('filter-empty-row') === -1 && cls.indexOf('admin-table-empty') === -1;
      }
      var dataRows = Array.prototype.filter.call(tbody.rows, isDataRow);
      var total = dataRows.length;
      var emptyRow = null;
      function showEmpty(visible, q) {
        if (q && visible === 0 && total > 0) {
          if (!emptyRow) {
            emptyRow = document.createElement('tr');
            emptyRow.className = 'filter-empty-row';
            emptyRow.innerHTML = '<td colspan="99" class="admin-table-empty">没有匹配的结果</td>';
          }
          if (!emptyRow.parentNode) tbody.appendChild(emptyRow);
        } else if (emptyRow && emptyRow.parentNode) {
          emptyRow.remove();
        }
      }
      input.addEventListener('input', function () {
        var q = input.value.trim().toLowerCase();
        var visible = 0;
        Array.prototype.forEach.call(tbody.rows, function (row) {
          if (!isDataRow(row)) return;
          var hit = !q || row.textContent.toLowerCase().indexOf(q) !== -1;
          row.style.display = hit ? '' : 'none';
          if (hit) visible++;
        });
        showEmpty(visible, q);
        if (countEl) {
          if (q && total > 1) {
            countEl.textContent = visible + ' / ' + total;
            countEl.classList.add('is-on');
          } else {
            countEl.textContent = '';
            countEl.classList.remove('is-on');
          }
        }
        // 通知分页器：搜索中展示全部命中行，清空后恢复分页
        table.dispatchEvent(new CustomEvent('lume:filter', { detail: { q: q } }));
      });
      // Esc 清空
      input.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') { input.value = ''; input.dispatchEvent(new Event('input')); }
      });
    });
  }

  /* ---------- 客户端分页（data-pageable 表格，与筛选联动） ---------- */
  function bindPagination() {
    document.querySelectorAll('table[data-pageable]').forEach(function (table) {
      var tbody = table.tBodies[0];
      if (!tbody) return;
      function isDataRow(row) {
        var cls = row.className || '';
        return cls.indexOf('filter-empty-row') === -1 && cls.indexOf('admin-table-empty') === -1;
      }
      var rows = Array.prototype.filter.call(tbody.rows, isDataRow);
      var per = parseInt(table.getAttribute('data-page-size'), 10) || 20;
      if (!rows.length) return;
      var page = 1;
      var wrap = table.parentNode;
      if (!wrap) return;
      var pager = document.createElement('div');
      pager.className = 'ui-pager';
      pager.innerHTML =
        '<span class="ui-pager-info"></span>' +
        '<div class="ui-pager-nav">' +
        '<button type="button" class="ui-pg-btn" data-pg="prev">‹ 上一页</button>' +
        '<span class="ui-pg-pos" data-pg-pos></span>' +
        '<button type="button" class="ui-pg-btn" data-pg="next">下一页 ›</button>' +
        '</div>';
      if (wrap.nextSibling) wrap.parentNode.insertBefore(pager, wrap.nextSibling);
      else wrap.parentNode.appendChild(pager);
      var info = pager.querySelector('.ui-pager-info');
      var pos = pager.querySelector('[data-pg-pos]');
      var total = rows.length;
      var pages = Math.max(1, Math.ceil(total / per));
      function render() {
        var start = (page - 1) * per;
        var end = Math.min(start + per, total);
        rows.forEach(function (row, i) {
          row.style.display = (i >= start && i < end) ? '' : 'none';
        });
        pos.textContent = page + ' / ' + pages;
        info.textContent = '共 ' + total + ' 条';
        pager.querySelector('[data-pg="prev"]').disabled = page <= 1;
        pager.querySelector('[data-pg="next"]').disabled = page >= pages;
        pager.style.display = total > per ? 'flex' : 'none';
      }
      pager.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-pg]');
        if (!btn) return;
        if (btn.getAttribute('data-pg') === 'prev') page = Math.max(1, page - 1);
        else page = Math.min(pages, page + 1);
        render();
      });
      table.addEventListener('lume:filter', function (e) {
        if (e.detail.q) {
          // 搜索态由筛选逻辑直接控制每行显隐，这里仅收起分页条
          pager.style.display = 'none';
        } else {
          page = 1;
          render();
        }
      });
      render();
    });
  }

  /* ---------- “/” 聚焦首个表格筛选框 ---------- */
  function bindFilterShortcut() {
    document.addEventListener('keydown', function (e) {
      var tag = (e.target && (e.target.tagName || '').toLowerCase()) || '';
      if (tag === 'input' || tag === 'textarea' || tag === 'select') return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === '/') {
        var first = document.querySelector('input[data-table-filter]');
        if (first) { e.preventDefault(); first.focus(); first.select(); }
      }
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

  /* ---------- 目录导入：全选 + 选中计数 ---------- */
  function bindSelectAll() {
    var master = document.getElementById('selectAllImport');
    var boxes = document.querySelectorAll('input[name="import"]:not(:disabled)');
    var countEl = document.getElementById('importCount');
    var submit = document.getElementById('importSubmit');
    function refresh() {
      if (!countEl && !submit) return;
      var checked = Array.prototype.filter.call(
        document.querySelectorAll('input[name="import"]:not(:disabled)'), function (cb) { return cb.checked; }).length;
      if (submit) {
        submit.disabled = checked === 0;
      }
      if (countEl) countEl.textContent = checked ? '已选 ' + checked + ' 项' : '';
    }
    if (master) {
      master.addEventListener('change', function () {
        document.querySelectorAll('input[name="import"]:not(:disabled)').forEach(function (cb) {
          cb.checked = master.checked;
        });
        refresh();
      });
    }
    document.querySelectorAll('input[name="import"]').forEach(function (cb) {
      cb.addEventListener('change', refresh);
    });
    // 校验主选框状态
    function syncMaster() {
      if (!master) return;
      var all = document.querySelectorAll('input[name="import"]:not(:disabled)');
      var checked = Array.prototype.filter.call(all, function (cb) { return cb.checked; }).length;
      master.checked = all.length > 0 && checked === all.length;
      master.indeterminate = checked > 0 && checked < all.length;
    }
    document.querySelectorAll('input[name="import"]').forEach(function (cb) {
      cb.addEventListener('change', syncMaster);
    });
    refresh();
  }

  /* ---------- 数字输入框：滚轮不改变数值（防误触） ---------- */
  function bindNumberWheelGuard() {
    document.addEventListener('wheel', function (e) {
      var t = e.target;
      if (t && t.tagName === 'INPUT' && t.type === 'number') t.blur();
    }, { passive: true });
  }

  /* ---------- 后台抽屉（移动端）：遮罩点击关闭 + 菜单项点击收起 + 滚动锁定 ---------- */
  function bindAdminDrawer() {
    var shell = document.querySelector('[data-admin-shell]');
    var btn = document.querySelector('[data-admin-menu]');
    if (!shell || !btn) return;
    var mq = window.matchMedia('(max-width: 800px)');
    function setOpen(open) {
      shell.classList.toggle('is-open', open);
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
      document.body.classList.toggle('ui-modal-lock', open && mq.matches);
    }
    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      setOpen(!shell.classList.contains('is-open'));
    });
    document.addEventListener('click', function (e) {
      if (!shell.classList.contains('is-open')) return;
      if (e.target.closest('.admin-sidebar') || e.target.closest('[data-admin-menu]')) return;
      setOpen(false);
    });
    shell.querySelectorAll('.admin-nav-item').forEach(function (a) {
      a.addEventListener('click', function () {
        if (mq.matches) setOpen(false);
      });
    });
    // 视口跨过断点时复位抽屉状态
    mq.addEventListener('change', function () { if (!mq.matches) setOpen(false); });
  }

  document.addEventListener('DOMContentLoaded', function () {
    toggle(document.querySelector('[data-site-menu]'), document.querySelector('[data-site-header]'), 'is-open');
    bindAdminDrawer();
    catalogSearch();
    catalogMenu();
    bindFormBehavior();
    bindCopy();
    bindTableFilter();
    bindPagination();
    bindFilterShortcut();
    bindDetailsAccordion();
    bindEscape();
    bindAutoDismiss();
    bindSelectAll();
    bindNumberWheelGuard();
  });
}());
