/**
 * Grenton page, Dashboards tab: dashboards from the pages of the myGrenton interface (#220).
 */
(function() {
    var root = document.getElementById('mgDashboards');
    var out = document.getElementById('mgResult');
    if (!root) return;

    var pl = document.documentElement.lang === 'pl';
    var T = pl ? {
        none: 'Brak zaimportowanego interfejsu myGrenton. Wgraj projekt Object Managera (.omp) albo paczkę diagnostyczną w zakładce OM Files.',
        page: 'Strona', widgets: 'Widgety', file: 'Plik', leftOut: 'pominięte',
        stNone: 'nie utworzony', stVclu: 'utworzony', stEdited: 'zmieniony przez Ciebie', stUser: 'Twój plik',
        addRequire: 'Ładuj dashboardy: dopisz require("dashboards/grenton") do modules/init.lua',
        required: 'modules/init.lua już ładuje te dashboardy.',
        create: 'Utwórz dashboardy', choose: 'Zaznacz co najmniej jedną stronę.',
        replaceQ: 'Te dashboardy były już utworzone. Zastąpić je?', replace: 'Zastąp', cancel: 'Anuluj',
        kept: 'Te pliki są Twoje, vclu ich nie zastąpi', written: 'Zapisano', reloaded: 'Lua przeładowane.',
        notLoaded: 'Pliki są zapisane, ale nie ładują się, dopóki modules/init.lua ich nie wczyta.',
        reloadFailed: 'Przeładowanie Lua nie powiodło się', moduleErrors: 'Błędy modułów', open: 'Otwórz',
        flat: 'Istnieje plik', flatTail: 'require("dashboards/grenton") wczytałby go zamiast katalogu. Zmień jego nazwę albo go usuń.'
    } : {
        none: 'No myGrenton interface imported. Upload the Object Manager project (.omp) or a diagnostic pack in the OM Files tab.',
        page: 'Page', widgets: 'Widgets', file: 'File', leftOut: 'left out',
        stNone: 'not created', stVclu: 'created', stEdited: 'changed by you', stUser: 'your file',
        addRequire: 'Load the dashboards: add require("dashboards/grenton") to modules/init.lua',
        required: 'modules/init.lua already loads these dashboards.',
        create: 'Create dashboards', choose: 'Pick at least one page.',
        replaceQ: 'These dashboards were created before. Replace them?', replace: 'Replace', cancel: 'Cancel',
        kept: 'These files are yours, vclu does not replace them', written: 'Written', reloaded: 'Lua reloaded.',
        notLoaded: 'The files are written but not loaded until modules/init.lua requires them.',
        reloadFailed: 'Lua reload failed', moduleErrors: 'Module errors', open: 'Open',
        flat: 'There is a file', flatTail: 'require("dashboards/grenton") would load it instead of the directory. Rename or remove it.'
    };
    var STATES = {
        none: ['secondary', T.stNone], vclu: ['success', T.stVclu],
        edited: ['warning', T.stEdited], user: ['warning', T.stUser]
    };
    var esc = VCLU.esc;

    function list(items) {
        return '<ul class="mb-0 small">' + items.map(function(i) { return '<li>' + esc(i) + '</li>'; }).join('') + '</ul>';
    }

    function render(data) {
        if (!data.interfaces || data.interfaces.length === 0) {
            root.innerHTML = '<div class="alert alert-secondary mb-0"><i class="bi bi-info-circle"></i> ' + esc(T.none) + '</div>';
            return;
        }
        var html = '';
        if (data.flatFile) {
            html += '<div class="alert alert-warning"><i class="bi bi-exclamation-triangle"></i> ' + esc(T.flat) +
                    ' <code>' + esc(data.flatFile) + '</code>: ' + esc(T.flatTail) + '</div>';
        }
        data.interfaces.forEach(function(inf) {
            html += '<form class="mg-interface mb-3" data-id="' + esc(inf.id) + '">';
            html += '<h6 class="mb-2"><i class="bi bi-phone"></i> ' + esc(inf.label) + '</h6>';
            html += '<div class="table-responsive"><table class="table table-sm align-middle mb-2"><thead><tr>' +
                    '<th></th><th>' + esc(T.page) + '</th><th>' + esc(T.widgets) + '</th><th>' + esc(T.file) + '</th></tr></thead><tbody>';
            inf.pages.forEach(function(p) {
                var st = STATES[p.state] || STATES.none;
                var mine = p.state === 'edited' || p.state === 'user';
                html += '<tr><td><input class="form-check-input" type="checkbox" value="' + esc(p.id) + '"' +
                        (mine ? ' disabled' : (p.state === 'none' ? ' checked' : '')) + '></td>';
                html += '<td>' + esc(p.label);
                if (p.leftOut.length > 0) {
                    html += '<details class="small text-warning"><summary>' + p.leftOut.length + ' ' + esc(T.leftOut) +
                            '</summary>' + list(p.leftOut) + '</details>';
                }
                html += '</td><td>' + p.widgets + '</td>';
                html += '<td><span class="badge bg-' + st[0] + '">' + esc(st[1]) + '</span> <code class="small">' + esc(p.file) + '</code>';
                if (p.state === 'vclu' || p.state === 'edited') {
                    html += ' <a class="small" href="/d/grenton_page_' + encodeURIComponent(p.id) + '">' + esc(T.open) + '</a>';
                }
                html += '</td></tr>';
            });
            html += '</tbody></table></div>';
            if (data.required) {
                html += '<p class="small text-muted"><i class="bi bi-check2"></i> ' + esc(T.required) + '</p>';
            } else {
                html += '<div class="form-check mb-2"><input class="form-check-input" type="checkbox" id="mgReq-' + esc(inf.id) +
                        '" name="addRequire"><label class="form-check-label small" for="mgReq-' + esc(inf.id) + '">' +
                        esc(T.addRequire) + '</label></div>';
            }
            html += '<button type="submit" class="btn btn-primary btn-sm"' + (data.flatFile ? ' disabled' : '') + '>' +
                    '<i class="bi bi-plus-square"></i> ' + esc(T.create) + '</button></form>';
        });
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
            out.innerHTML = '<div class="alert alert-warning">' + esc(T.choose) + '</div>';
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
                out.innerHTML = '<div class="alert alert-warning"><p class="mb-2">' + esc(T.replaceQ) + '</p>' + list(d.ask) +
                    '<div class="mt-2"><button type="button" class="btn btn-warning btn-sm" id="mgReplace">' + esc(T.replace) +
                    '</button> <button type="button" class="btn btn-secondary btn-sm" id="mgCancel">' + esc(T.cancel) + '</button></div></div>';
                document.getElementById('mgReplace').onclick = function() { create(form, true); };
                document.getElementById('mgCancel').onclick = function() { out.innerHTML = ''; };
                return;
            }
            if (d.kept) {
                out.innerHTML = '<div class="alert alert-warning">' + esc(T.kept) + ':' + list(d.kept) + '</div>';
                return;
            }
            if (d.error) {
                out.innerHTML = '<div class="alert alert-danger"><i class="bi bi-x-circle"></i> ' + esc(d.error) + '</div>';
                return;
            }
            var html = '<div class="alert alert-success"><i class="bi bi-check-circle"></i> ' + esc(T.written) + ': ' +
                       d.written.map(function(f) { return '<code>' + esc(d.dir + '/' + f) + '</code>'; }).join(', ');
            if (d.reloadError) {
                html += '<div class="text-danger mt-2">' + esc(T.reloadFailed) + ': ' + esc(d.reloadError) + '</div>';
            } else if (d.reloaded) {
                html += '<div class="mt-2">' + esc(T.reloaded) + ' ' + d.dashboardsIds.map(function(id) {
                    return '<a href="/d/' + encodeURIComponent(id) + '">' + esc(id) + '</a>';
                }).join(', ') + '</div>';
            } else if (!d.required) {
                html += '<div class="mt-2">' + esc(T.notLoaded) + '</div>';
            }
            if (d.moduleErrors) {
                html += '<div class="text-warning mt-2">' + esc(T.moduleErrors) + ':' + list(d.moduleErrors) + '</div>';
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
