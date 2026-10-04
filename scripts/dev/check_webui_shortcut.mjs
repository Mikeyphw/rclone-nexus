import fs from 'node:fs';

const config = JSON.parse(fs.readFileSync('module/webroot/config.json', 'utf8'));
const html = fs.readFileSync('module/webroot/index.html', 'utf8');
const icon = fs.readFileSync('module/webroot/icon.png');

function require(value, message) {
  if (!value) throw new Error(message);
}

require(config.modId === 'rclone_nexus', 'WebUI-X shortcut config must bind to rclone_nexus');
require(config.title === 'Rclone Nexus', 'WebUI-X shortcut title must be Rclone Nexus');
require(config.icon === 'icon.png', 'WebUI-X shortcut icon must be icon.png');
require(Object.keys(config).every((key) => ['modId', 'title', 'icon'].includes(key)), 'shortcut config must stay minimal and capability-neutral');
require(icon.length > 1024, 'shortcut icon is unexpectedly small');
require(icon.subarray(0, 8).equals(Buffer.from([0x89,0x50,0x4e,0x47,0x0d,0x0a,0x1a,0x0a])), 'shortcut icon is not a PNG');
require(icon.readUInt32BE(16) === 512 && icon.readUInt32BE(20) === 512, 'shortcut icon must be 512x512');
require(html.includes('<link rel="icon" href="icon.png" type="image/png">'), 'WebUI favicon/shortcut icon link missing');
console.log('WebUI-X/MMRL shortcut metadata contract PASS');
