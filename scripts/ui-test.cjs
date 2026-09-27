// Run against `go run ./cmd/mock-cliproxy`; never contacts a real OAuth service.
const {chromium} = require('playwright');
const assert = require('node:assert/strict');
(async () => {
  const browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/usr/bin/chromium', headless:true, args:['--no-sandbox']});
  try {
    const context = await browser.newContext();
    await context.route('https://**/*', route => route.fulfill({status:200, contentType:'text/html', body:'<p>Mock provider consent. Return to the dashboard.</p>'}));
    const page = await context.newPage();
    const errors=[];page.on('pageerror',e=>errors.push(e.message));
    await page.goto('http://127.0.0.1:18318/');
    await page.getByLabel('WebUI password').fill('development-only-password');
    await page.getByRole('button',{name:'Sign in →'}).click();
    await page.getByText('mock-model',{exact:true}).waitFor();
    assert.equal(await page.locator('#status').textContent(),'Running');
    await page.getByRole('button',{name:'Providers',exact:true}).click();
    const card=page.locator('#provider-cards article').filter({has:page.getByRole('heading',{name:'OpenAI / ChatGPT',exact:true})});
    await card.getByRole('button',{name:'Connect',exact:true}).click();
    await page.locator('#callback').fill('http://localhost:1455/auth/callback?state=wrong&code=mock-code');
    await page.getByRole('button',{name:'Complete connection'}).click();
    await page.getByText('Callback rejected; use the complete URL for this login',{exact:true}).waitFor();
    await page.locator('#callback').fill('http://localhost:1455/auth/callback?state=mock-state&code=mock-code');
    await page.getByRole('button',{name:'Complete connection'}).click();
    await page.getByText('OpenAI connected',{exact:true}).waitFor();
    await page.getByText('demo@example.invalid · plus · active',{exact:true}).waitFor();
    const text=await page.locator('body').innerText();assert(!text.includes('MUST-NOT-LEAK'));assert(!text.includes('mock-management-only'));
    await page.getByRole('button',{name:'Disable',exact:true}).click();
    await page.getByRole('button',{name:'Enable',exact:true}).waitFor();
    page.once('dialog',d=>d.dismiss());
    await page.getByRole('button',{name:'Disconnect',exact:true}).click();
    assert.equal(await page.locator('#accounts article').count(),1);
    page.once('dialog',d=>d.accept());
    await page.getByRole('button',{name:'Disconnect',exact:true}).click();
    await page.getByText('No accounts connected yet.',{exact:true}).waitFor();
    await page.getByRole('button',{name:'API Access',exact:true}).click();
    const before=await page.locator('#access .key-hint').textContent();
    page.once('dialog',d=>d.accept());
    await page.getByRole('button',{name:'Regenerate API Key',exact:true}).click();
    await page.getByText('New key verified. Copy it into your clients.',{exact:true}).waitFor();
    assert.notEqual(await page.locator('#access .key-hint').textContent(),before);
    await page.getByRole('button',{name:'Overview',exact:true}).click();
    await page.screenshot({path:process.env.CPA_SCREENSHOT || '/tmp/cpa-dashboard.png',fullPage:true});
    await page.getByRole('button',{name:'Sign out',exact:true}).click();
    await page.getByRole('button',{name:'Sign in →'}).waitFor();
    assert.deepEqual(errors,[]);
    console.log('PASS: Chromium login, remote callback rejection/success, safe accounts, disable/delete confirmation, key rotation, logout. Mock OAuth only.');
  } finally {await browser.close();}
})().catch(e=>{console.error(e.message);process.exit(1)});
