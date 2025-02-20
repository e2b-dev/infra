import { expect } from "jsr:@std/expect";

export function extractTemplateID(listOut: string, templateName: string) {
    return listOut.split('\n').reduce((acc: string, line) => {
        if (line.includes(templateName)) {
            const items = line.split(' ')
            const id = items[2]
            return id
        }
        return acc
    }, '')
}

Deno.test("extractTemplateID", () => {
    const output = `
    Access   Template ID           Template Name                                       vCPUs  RAM MiB            Created by  Created at 
Private  veiohd78xjs3ibuaju57  test-template-4504da89-35a8-4a40-9d30-d7aa40080c77      2     1024  robert.wendt@e2b.dev   2/19/2025 
Private  1bcr85b1kh4h87yxfsn5  test-template-f7f57822-1500-4d35-9af1-2638bcf77952      2     1024  robert.wendt@e2b.dev   2/19/2025 
`
    const templateID = extractTemplateID(output, 'test-template-4504da89-35a8-4a40-9d30-d7aa40080c77')
    // console.log(templateID)
    expect(templateID).toBe('veiohd78xjs3ibuaju57')
})


