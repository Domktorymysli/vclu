/**
 * Grenton page, Dashboards tab: dashboards from the pages of the myGrenton interface (#220).
 * Texts come from web/locales (mygrenton.*) as window.MG_T.
 */
(function() {
    var root = document.getElementById('mgDashboards');
    var out = document.getElementById('mgResult');
    if (!root) return;

    var T = window.MG_T || {};
    function t(key) { return T[key] || key; }
    var STATES = {
        none: ['secondary', 'st_none'], vclu: ['success', 'st_vclu'],
        edited: ['warning', 'st_edited'], user: ['warning', 'st_user']
    };
    var esc = VCLU.esc;
    // for attribute values: esc does not escape quotes
    function attr(s) { return esc(String(s)).replace(/"/g, '&quot;').replace(/'/g, '&#39;'); }

    function list(items) {
        return '<ul class="mb-0 small">' + items.map(function(i) { return '<li>' + esc(i) + '</li>'; }).join('') + '</ul>';
    }

    function badge(state) {
        var st = STATES[state] || STATES.user;
        return '<span class="badge bg-' + st[0] + '">' + esc(t(st[1])) + '</span>';
    }

    function widgets(p) {
        var html = '<details class="small"><summary>' + p.widgets.length + ' ' + esc(t('widgets').toLowerCase()) + '</summary><ul class="mb-0">';
        p.widgets.forEach(function(w) {
            html += '<li><code>' + esc(w.kind) + '</code> ' + esc(w.label);
            if (w.reads.length) html += ' <span class="text-muted">' + esc(t('reads')) + ' ' + esc(w.reads.join(', ')) + '</span>';
            if (w.does.length) html += ' <span class="text-muted">' + esc(t('does')) + ' ' + esc(w.does.join(', ')) + '</span>';
            html += '</li>';
        });
        return html + '</ul></details>';
    }

    function render(data) {
        if (!data.interfaces || data.interfaces.length === 0) {
            root.innerHTML = '<div class="alert alert-secondary mb-0"><i class="bi bi-info-circle"></i> ' + esc(t('none')) + '</div>';
            return;
        }
        var html = '';
        if (data.flatFile) {
            html += '<div class="alert alert-warning"><i class="bi bi-exclamation-triangle"></i> ' + esc(t('flat')) +
                    ' <code>' + esc(data.flatFile) + '</code>: ' + esc(t('flat_tail')) + '</div>';
        }
        data.interfaces.forEach(function(inf, n) {
            html += '<form class="mg-interface mb-3" data-id="' + attr(inf.id) + '">';
            html += '<h6 class="mb-2"><i class="bi bi-phone"></i> ' + esc(inf.label) + '</h6>';
            html += '<div class="table-responsive"><table class="table table-sm align-middle mb-2"><thead><tr>' +
                    '<th></th><th>' + esc(t('page')) + '</th><th>' + esc(t('widgets')) + '</th><th>' + esc(t('file')) + '</th></tr></thead><tbody>';
            inf.pages.forEach(function(p) {
                var mine = p.state === 'edited' || p.state === 'user';
                html += '<tr><td><input class="form-check-input" type="checkbox" value="' + attr(p.id) + '"' +
                        (mine ? ' disabled' : (p.state === 'none' ? ' checked' : '')) + '></td>';
                html += '<td>' + esc(p.label);
                if (p.leftOut.length > 0) {
                    html += '<details class="small text-warning"><summary>' + p.leftOut.length + ' ' + esc(t('left_out')) +
                            '</summary>' + list(p.leftOut) + '</details>';
                }
                html += '</td><td>' + widgets(p) + '</td>';
                html += '<td>' + badge(p.state) + ' <code class="small">' + esc(p.file) + '</code>';
                if (p.state !== 'none') {
                    html += ' <a class="small" href="/d/imported_' + encodeURIComponent(p.id) + '">' + esc(t('open')) + '</a>';
                }
                html += '</td></tr>';
            });
            html += '</tbody></table></div>';
            if (data.required) {
                html += '<p class="small text-muted"><i class="bi bi-check2"></i> ' + esc(t('required')) + '</p>';
            } else {
                html += '<div class="form-check mb-2"><input class="form-check-input" type="checkbox" id="mgReq' + n +
                        '" name="addRequire"><label class="form-check-label small" for="mgReq' + n + '">' +
                        esc(t('add_require')) + '</label></div>';
            }
            html += '<button type="submit" class="btn btn-primary btn-sm"' + (data.flatFile ? ' disabled' : '') + '>' +
                    '<i class="bi bi-plus-square"></i> ' + esc(t('create')) + '</button></form>';
        });
        if (data.orphans && data.orphans.length > 0) {
            html += '<div class="alert alert-secondary small"><i class="bi bi-info-circle"></i> ' + esc(t('orphans')) +
                    list(data.orphans.map(function(o) { return data.dir + '/' + o.file; })) + '</div>';
        }
        root.innerHTML = html;
        root.querySelectorAll('form.mg-interface').forEach(function(f) {
            f.addEventListener('submit', function(e) {
                e.preventDefault();
                create(f, false);
            });
        });
    }

    function create(form, replace) {
        var pages = Array.prototype.map.call(form.querySelectorAll('tbody input:checked'), function(i) { return i.value; });
        if (pages.length === 0) {
            out.innerHTML = '<div class="alert alert-warning">' + esc(t('choose')) + '</div>';
            return;
        }
        var req = form.querySelector('input[name="addRequire"]');
        var body = { interface: form.dataset.id, pages: pages, replace: replace, addRequire: !!(req && req.checked) };
        var btn = form.querySelector('button[type="submit"]');
        btn.disabled = true;
        fetch('/api/grenton/dashboards', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        })
        .then(function(r) { return r.json().then(function(d) { return { status: r.status, data: d }; }); })
        .then(function(res) {
            var d = res.data;
            if (res.status === 409 && d.ask) {
                out.innerHTML = '<div class="alert alert-warning"><p class="mb-2">' + esc(t('replace_q')) + '</p>' + list(d.ask) +
                    '<div class="mt-2"><button type="button" class="btn btn-warning btn-sm" id="mgReplace">' + esc(t('replace')) +
                    '</button> <button type="button" class="btn btn-secondary btn-sm" id="mgCancel">' + esc(t('cancel')) + '</button></div></div>';
                document.getElementById('mgReplace').onclick = function() { create(form, true); };
                document.getElementById('mgCancel').onclick = function() { out.innerHTML = ''; };
                return;
            }
            if (d.kept) {
                out.innerHTML = '<div class="alert alert-warning">' + esc(t('kept')) + ':' + list(d.kept) + '</div>';
                return;
            }
            if (d.error) {
                out.innerHTML = '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' + esc(d.error) + '</div>';
                return;
            }
            var html = '<div class="alert alert-success"><i class="bi bi-check-circle"></i> ' + esc(t('written')) + ': ' +
                       d.written.map(function(f) { return '<code>' + esc(d.dir + '/' + f) + '</code>'; }).join(', ');
            if (d.initKept && d.initLines.length > 0) {
                html += '<div class="mt-2">' + esc(t('init_kept')) + '<pre class="small mb-0">' + esc(d.initLines.join('\n')) + '</pre></div>';
            }
            if (d.requireLine) {
                html += '<div class="mt-2">' + esc(t('require_manual')) + '<pre class="small mb-0">' + esc(d.requireLine) + '</pre></div>';
            }
            if (d.reloadError) {
                html += '<div class="text-danger mt-2">' + esc(t('reload_failed')) + ': ' + esc(d.reloadError) + '</div>';
            } else if (d.reloaded) {
                html += '<div class="mt-2">' + esc(t('reloaded')) + ' ' + d.dashboardIds.map(function(id) {
                    return '<a href="/d/' + encodeURIComponent(id) + '">' + esc(id) + '</a>';
                }).join(', ') + '</div>';
            } else if (!d.required) {
                html += '<div class="mt-2">' + esc(t('not_loaded')) + '</div>';
            }
            if (d.pageErrors) {
                html += '<div class="text-warning mt-2">' + esc(t('page_errors')) + ':' + list(d.pageErrors) + '</div>';
            }
            if (d.moduleErrors) {
                html += '<div class="text-warning mt-2">' + esc(t('module_errors')) + ':' + list(d.moduleErrors) + '</div>';
            }
            out.innerHTML = html + '</div>';
            load();
        })
        .catch(function(err) {
            out.innerHTML = '<div class="alert alert-danger">' + esc(err.message) + '</div>';
        })
        .finally(function() { btn.disabled = false; });
    }

    function load() {
        fetch('/api/grenton/dashboards')
            .then(function(r) { return r.json(); })
            .then(function(d) {
                if (d.error) {
                    root.innerHTML = '<div class="alert alert-danger">' + esc(d.error) + '</div>';
                    return;
                }
                render(d);
            })
            .catch(function(err) { root.innerHTML = '<div class="alert alert-danger">' + esc(err.message) + '</div>'; });
    }

    load();
})();
