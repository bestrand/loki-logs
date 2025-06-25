class LogImporter {
    constructor() {
        this.initializeEventListeners();
    }

    initializeEventListeners() {
        const pasteForm = document.getElementById('pasteForm');
        const fileForm = document.getElementById('fileForm');

        if (pasteForm) {
            pasteForm.addEventListener('submit', this.handlePasteSubmit.bind(this));
        }

        if (fileForm) {
            fileForm.addEventListener('submit', this.handleFileSubmit.bind(this));
        }
    }

    async handlePasteSubmit(event) {
        event.preventDefault();
        
        const formData = new FormData(event.target);
        const logText = formData.get('logText');

        if (!logText?.trim()) {
            this.showResult('pasteResult', 'Please enter some log text to import.', true);
            return;
        }

        const button = event.target.querySelector('button[type="submit"]');
        this.setButtonLoading(button, true);

        try {
            const response = await fetch('/api/import-text', {
                method: 'POST',
                body: formData
            });

            const result = await response.text();
            this.showResult('pasteResult', result, !response.ok);
        } catch (error) {
            this.showResult('pasteResult', `Network error: ${error.message}`, true);
        } finally {
            this.setButtonLoading(button, false);
        }
    }

    async handleFileSubmit(event) {
        event.preventDefault();
        
        const formData = new FormData(event.target);
        const files = formData.getAll('files');

        if (files.length === 0 || files[0].size === 0) {
            this.showResult('fileResult', 'Please select at least one file to upload.', true);
            return;
        }

        for (const file of files) {
            if (file.size === 0) {
                this.showResult('fileResult', `File "${file.name}" is empty. Please select files with content.`, true);
                return;
            }
        }

        const button = event.target.querySelector('button[type="submit"]');
        this.setButtonLoading(button, true);

        try {
            const response = await fetch('/api/upload-files', {
                method: 'POST',
                body: formData
            });

            const result = await response.text();
            this.showResult('fileResult', result, !response.ok);
            
            // Clear file input on success
            if (response.ok) {
                const fileInput = event.target.querySelector('input[type="file"]');
                if (fileInput) {
                    fileInput.value = '';
                }
            }
        } catch (error) {
            this.showResult('fileResult', `Network error: ${error.message}`, true);
        } finally {
            this.setButtonLoading(button, false);
        }
    }

    showResult(elementId, message, isError = false) {
        const container = document.getElementById(elementId);
        if (!container) return;

        const resultBox = document.createElement('div');
        resultBox.className = `result-box ${isError ? 'result-error' : 'result-success'}`;
        resultBox.textContent = message;

        container.innerHTML = '';
        container.appendChild(resultBox);

        resultBox.scrollIntoView({ 
            behavior: 'smooth', 
            block: 'nearest' 
        });
    }

    setButtonLoading(button, isLoading) {
        if (!button) return;

        const textSpan = button.querySelector('.btn-text');
        const spinner = button.querySelector('.spinner');

        button.disabled = isLoading;
        
        if (textSpan) {
            textSpan.style.opacity = isLoading ? '0' : '1';
        }
        
        if (spinner) {
            spinner.style.display = isLoading ? 'block' : 'none';
        }
    }
}
