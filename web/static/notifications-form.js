function notifChannels(basePath) {
    return {
        basePath: basePath,
        showForm: false,
        editId: 0,
        advancedNotifSettings: false,
        formData: { name: '', type: 'webhook', enabled: true, settings_json: '{}' },
        events: { created: true, resolved: true, acknowledged: false, reminder: true, changed: false, certChanged: false },
        webhook: { url: '', secret: '' },
        telegram: { bot_token: '', chat_id: '' },
        discord: { webhook_url: '' },
        slack: { webhook_url: '', channel: '' },
        email: { host: '', port: 587, username: '', password: '', from: '', to: '', tls_mode: 'starttls', cc: '', bcc: '' },
        ntfy: { server_url: '', topic: '', priority: 3, tags: '', click_url: '' },
        teams: { webhook_url: '' },
        pagerduty: { routing_key: '' },
        opsgenie: { api_key: '', region: '' },
        pushover: { user_key: '', app_token: '', priority: '0', sound: '', device: '' },
        googlechat: { webhook_url: '' },
        matrix: { homeserver: '', access_token: '', room_id: '' },
        gotify: { server_url: '', app_token: '', priority: 5 },
        get formAction() {
            return this.editId ? this.basePath + '/notifications/' + this.editId : this.basePath + '/notifications';
        },
        resetForm() {
            this.editId = 0;
            this.advancedNotifSettings = false;
            this.formData = { name: '', type: 'webhook', enabled: true, settings_json: '{}' };
            this.events = { created: true, resolved: true, acknowledged: false, reminder: true, changed: false, certChanged: false };
            this.webhook = { url: '', secret: '' };
            this.telegram = { bot_token: '', chat_id: '' };
            this.discord = { webhook_url: '' };
            this.slack = { webhook_url: '', channel: '' };
            this.email = { host: '', port: 587, username: '', password: '', from: '', to: '', tls_mode: 'starttls', cc: '', bcc: '' };
            this.ntfy = { server_url: '', topic: '', priority: 3, tags: '', click_url: '' };
            this.teams = { webhook_url: '' };
            this.pagerduty = { routing_key: '' };
            this.opsgenie = { api_key: '', region: '' };
            this.pushover = { user_key: '', app_token: '', priority: '0', sound: '', device: '' };
            this.googlechat = { webhook_url: '' };
            this.matrix = { homeserver: '', access_token: '', room_id: '' };
            this.gotify = { server_url: '', app_token: '', priority: 5 };
        },
        editChannel(ch) {
            this.resetForm();
            this.editId = ch.id;
            this.formData.name = ch.name;
            this.formData.type = ch.type;
            this.formData.enabled = ch.enabled;
            this.formData.settings_json = JSON.stringify(ch.settings || {});
            this.events = { created: false, resolved: false, acknowledged: false, reminder: false, changed: false, certChanged: false };
            if (ch.events) {
                ch.events.forEach(e => {
                    if (e === 'incident.created') this.events.created = true;
                    if (e === 'incident.resolved') this.events.resolved = true;
                    if (e === 'incident.acknowledged') this.events.acknowledged = true;
                    if (e === 'incident.reminder') this.events.reminder = true;
                    if (e === 'content.changed') this.events.changed = true;
                    if (e === 'cert.changed') this.events.certChanged = true;
                });
            }
            let s = ch.settings || {};
            switch (ch.type) {
                case 'webhook': this.webhook = { url: s.url || '', secret: s.secret || '' }; break;
                case 'telegram': this.telegram = { bot_token: s.bot_token || '', chat_id: s.chat_id || '' }; break;
                case 'discord': this.discord = { webhook_url: s.webhook_url || '' }; break;
                case 'slack': this.slack = { webhook_url: s.webhook_url || '', channel: s.channel || '' }; break;
                case 'email': this.email = { host: s.host || '', port: s.port || 587, username: s.username || '', password: s.password || '', from: s.from || '', to: (s.to || []).join(', '), tls_mode: s.tls_mode || 'starttls', cc: (s.cc || []).join(', '), bcc: (s.bcc || []).join(', ') }; break;
                case 'ntfy': this.ntfy = { server_url: s.server_url || '', topic: s.topic || '', priority: String(s.priority || 3), tags: s.tags || '', click_url: s.click_url || '' }; break;
                case 'teams': this.teams = { webhook_url: s.webhook_url || '' }; break;
                case 'pagerduty': this.pagerduty = { routing_key: s.routing_key || '' }; break;
                case 'opsgenie': this.opsgenie = { api_key: s.api_key || '', region: s.region || '' }; break;
                case 'pushover': this.pushover = { user_key: s.user_key || '', app_token: s.app_token || '', priority: String(s.priority || 0), sound: s.sound || '', device: s.device || '' }; break;
                case 'googlechat': this.googlechat = { webhook_url: s.webhook_url || '' }; break;
                case 'matrix': this.matrix = { homeserver: s.homeserver || '', access_token: s.access_token || '', room_id: s.room_id || '' }; break;
                case 'gotify': this.gotify = { server_url: s.server_url || '', app_token: s.app_token || '', priority: String(s.priority || 5) }; break;
            }
            this.showForm = true;
        }
    };
}
